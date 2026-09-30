# AGENTS.md — piCVert

A single-page CV engine (Go) plus the service around it. The one invariant the
whole codebase defends:

> **The layout is computed once**, and both renderings — HTML and PDF — are
> painted from that same frame, with the line breaks already decided.

Anything that would let one output derive geometry the other did not is a bug,
however convenient. Read `README.md` for the why, `docs/DESIGN.md` for which
patterns are used and which are deliberately absent, `docs/STATUS.md` for known
gaps and the list of faults this codebase has already paid for.

## Commands

Everything below runs fine on this workstation (Go 1.26 is installed); only the
full CI rehearsal goes to the build host.

```bash
go build -o picvert ./cmd/picvert   # the whole build — no Node, no CGO
go test ./...
go vet ./... && gofmt -l .          # gofmt is enforced by CI, not advisory
go generate ./...                   # rebundle the interface after editing internal/server/ui
npm run typecheck                   # tsc --noEmit; the ONLY step needing Node
```

```bash
./picvert fit     --profile examples/jean-dupont
./picvert render  --profile examples/jean-dupont --out cv.html
./picvert pdf     --profile examples/jean-dupont --out cv.pdf
./picvert preview --profile examples/jean-dupont   # / and /cv.pdf
./picvert serve                                    # :3000 public, :3001 admin
```

Other subcommands: `new`, `passwd`, `config`, `backup`, `restore`, `version`.

**Before every push: `scripts/preflight.sh`.** It clones the *committed* tree
(not the working directory — a formatted-but-uncommitted copy once let a gofmt
failure reach GitHub), rsyncs it to the build host and runs the whole of CI
plus extras: race detector on `internal/layout` and `internal/lease`, the test
suite pinned to **one contended core** via `taskset` (GOMAXPROCS=2 alone did
*not* reproduce two real CI failures), all six cross-compile targets, `sh -n`
on the installer, and a standalone run of the binary in an empty directory.

`scripts/devdeploy.sh` builds on the build host and pushes the binary to the
appliance in ~20s, bypassing the release workflow. It replaces the binary only:
config, data and unit files are the installer's job.

## Generated-but-committed artefacts

Two directories are build output *and* are committed, so a clone builds with
the Go toolchain alone. CI rebuilds both and fails on any difference.

| path | produced by | trigger |
|---|---|---|
| `internal/server/web/assets/` | `tools/bundle` (esbuild, own Go module) via `go generate ./...` | any edit under `internal/server/ui/` |
| `fonts/*.subset.ttf`, `*.woff2` | `scripts/gen-webfonts.py` | changing the shipped faces (needs Python + fonttools; rare) |

`internal/assets/{templates,fonts}` are **real copies** of `templates/` and
`fonts/`, maintained by `scripts/embed-assets.sh`, because `go:embed` cannot
reach outside its own directory and does not follow symlinks. This script is
*not* wired into `go generate` — run it by hand after adding or editing a
template, or the binary ships the old one. Embedded assets are a **fallback**:
a `templates/` directory beside the binary wins; both are read through `io/fs`.

Asset URLs are content-hashed (`app-7F3A21C8.js`) and resolved through
`web/assets/manifest.json`; templates call the `asset` func, never a filename.

## Architecture

```
cv.json ─► engine.Engine ─► layout ─► Fitted frame ─┬─► internal/html  (painter)
                                                    └─► internal/pdf   (painter + embedded cv.json)
```

- **`internal/engine`** — the pipeline, assembled in exactly one place so `fit`,
  `render`, `pdf` and `serve` cannot compose the steps differently. Caches
  parsed font faces per template directory.
- **`internal/layout`** — the engine proper. `Painter` (visitor, one method per
  node shape) is the emitter seam; `Layouter` (strategy, registered by display
  kind in a map) is the per-kind algorithm seam. `Engine.Layout` is three fixed
  passes: **inherit, measure (bottom-up), place (top-down)**. Glyph advances are
  cached per face. Font metrics, kerning included, come from the actual font
  file — nothing estimates.
- **`internal/theme`** — palette, named styles and composition, all data.
  The composition language has exactly `repeat`, `when`, `{field}`, `styleBy`,
  `widthFrom`. Do not add a sixth.
- **`internal/fields`** — *the* description of a CV. Closed set of kinds
  (`text rich icon number bool enum photo list group`). The validator walks it,
  the editor generates forms from it, template manifests compose from it. It is
  mirrored in `internal/server/ui/model/` TypeScript; the two must agree field
  for field.
- **`internal/store`** — the single door every write goes through: validate,
  then atomic temp-file + rename, `.bak` alongside, optional git commit.
  `cv.json` is the only source of truth; nothing is built ahead of time.
- **`internal/server`** — public port (viewer, `/e/{token}` share links, editor,
  `/api/`) and admin port (`admin.go`). Routes use Go 1.22 method+pattern mux
  (`"GET /p/{slug}/cv.pdf"`). Supporting packages: `tokens`, `access`,
  `ownership`, `lease`, `quota`, `security`, `diff`, `metrics`, `turnstile`.
- **`internal/templates`** — discovery, validation, and the conformance kit.
- **`cmd/picvert`** — subcommand dispatch in `main.go`; `version` is stamped at
  build time via `-ldflags`.

### Adding a template

Create `templates/<name>/` with `template.json`, `theme.json`, `icons.json`,
`cover.svg`. No Go changes. Then run `scripts/embed-assets.sh`. The conformance
kit (`internal/templates/conformance_test.go`) walks `templates/` and tests it
automatically against a document **synthesised from the field tree** — it checks
four contracts that all fail silently: every accepted section type is composed,
every block is named (or overflow cannot be reported), the page reads as
`[header…, body]`, and every declared font is actually shipped.

## Where it runs

One appliance: **LXC container 405** on a Proxmox host reached as
`root@10.0.0.2`. The service lives at `/opt/picvert/picvert`, data under the
configured `PICVERT_DATA`, health on `:3000/healthz`.

Two ways in, for two different purposes:

| | path | when |
|---|---|---|
| **release** | tag `v*` → GitHub Actions → published artefact → the container downloads it | anything anybody else will run |
| **`scripts/devdeploy.sh`** | build on dev-build → pull here → push via `pct push` | "does this fix work on the box" |

The release path is authoritative: `release.yml` re-runs the whole of CI first,
because a tag is the one build nobody gets to re-run after the fact, and
`deploy/install.sh` checks the binary against its published digest.

`devdeploy.sh` exists because that round trip takes ~5 minutes and this takes
~20 seconds. Three facts about it that are not guessable:

- **The binary goes the long way round** — dev-build and the container cannot
  reach each other (different networks); only this workstation talks to both.
- **It is pushed as `picvert.new` then renamed**, because replacing a running
  binary in place gives "Text file busy" on this path every time.
- **It replaces the binary and nothing else.** Config, data and service files
  are the installer's job. So a change that needs a new config key or a new
  unit file will *not* work via devdeploy — use a release.

It restarts via `rc-service` falling back to `systemctl` (the container is
Alpine; both unit kinds ship in `deploy/`), then prints `version` and health.
A `dev-<sha>[-dirty]` version string means devdeploy, never a release.

Because the interface is **embedded in the binary**, server and editor upgrade
atomically — which is what made the private-link URL change a safe clean break.
Anything *outside* the binary that called the old `/api/p/<slug>` form with an
`X-CV-Token` header (scripts, bookmarks, agent loops) needs updating to the
`/e/<token>/api/…` form below.

## Editing a CV over HTTP

Everything private hangs off the link. The token **is** the credential and it
lives in the path, so there is no header to set and no slug to discover:

```sh
T=$(…)                      # the token from `picvert new`, or the admin page
B=http://localhost:3000/e/$T

curl -s      "$B/info"                          # slug, mode, languages, read link
curl -s      "$B/api/cv"                        # the document, plus an ETag
curl -s -X PUT --json @cv.json "$B/api/cv"      # the whole document
curl -s -X PATCH --json '{"name":"X"}' "$B/api/identity"
curl -s      "$B/api/fit"                       # does it still hold one page
curl -s -X POST --json @draft.json "$B/api/preview"   # an UNSAVED document
```

Also under `$B/api/`: `meta`, `template`, `sections/{id}`, `sections/order`,
`sections/{id}/order`, `history`, `languages`, `photo`, `lease`, and
`DELETE cv`. Pages are `$B/`, `$B/cv.html`, `$B/cv.pdf`, `$B/photo`, `$B/edit/`.

Four things that are not obvious:

- **A read link is refused at the door** for any non-GET, and for `/edit/`.
- **No lease is needed.** `requireLease` lets a caller presenting no holder
  through on purpose (`internal/server/lease.go:225`), so scripts work. Safety
  comes from the revision instead.
- **Send the ETag back as `If-Match`.** Omit it and your write always wins;
  send it and a stale write gets **409** rather than clobbering a live editor.
- **A save does not lay the page out.** `PUT` tells you nothing about whether
  the CV still fits — that was a 400x cost per keystroke. Ask `/api/fit`
  afterwards, or test the change with `/api/preview` before saving it.

Status codes carry meaning: 400 the document is wrong, 409 someone got there
first, 423 a browser holds the lease, 507 out of quota.

## Conventions and gotchas

- **Comments explain why, at length.** Package docs open with the fault that
  motivated the package, often under a `# WHY THIS EXISTS` heading. Match that
  register; do not strip existing rationale when editing around it.
- **A document travels as `map[string]any`** so unknown properties survive a
  round trip. Go type assertions match the *exact* dynamic type, so always go
  through `document.AsObject` — a named map type does not satisfy
  `.(map[string]any)`.
- **Measurement is one-sided**: the engine may measure text wider than it will
  be drawn, never narrower. Never "improve" accuracy in the narrowing direction.
- **Font face lookup must be deterministic and identically ranked in both the
  measurement and the painter** (posture outranks weight; no-weight means the
  regular face by name). Map iteration order once made the same CV lay out
  differently run to run.
- **PDF text state outlives `BT`/`ET`.** `Tc`, and anything like it, must be
  written or reset explicitly. Faults of this class extract as perfect text and
  are only visible in the geometry — use `scripts/pdf-overlap.py` (feed it
  `mutool draw -F stext`) to check; threshold 0.6 pt, zero pairs is the only
  acceptable answer. Also: an unmatched `q` is not an error, it silently clips
  the rest of the page.
- **Fonts are held as bytes, not as a path.** A face that re-reads its file can
  embed different bytes than it parsed, and a subsetter renumbers glyphs.
- **The page size is the engine's (794×1123), not the theme's.** A page that
  sizes to its content makes the fit check unable to report anything.
- **The style vocabulary and node kinds are closed**, same reasoning as fields.
  A property one painter understands and the other does not is the exact
  divergence this project exists to remove. (`shadow` is the documented
  exception.)
- **The admin port is the one place a slug is still a path parameter**
  (`/api/p/{slug}` on port 3001), and it stays that way. Its three operations on
  a named CV cannot be expressed through a link: rotation exists *because* a
  link leaked, so accepting that link would let whoever it leaked to revoke it
  first; a trashed CV's tokens move into the trash with its directory, so
  restore has no link to present; and creation has no token yet. Routing these
  through links would also move destructive capability onto the proxied port.
  Here the password says who and the slug says which — one fact each, nothing
  to disagree.
- **The admin port has no access control by design** — its protection is
  topological, it is never proxied outwards. Do not "fix" this with a password.
- **`innerHTML` appears nowhere** in the interface; `lib/dom.ts` sets
  `textContent`. That is the entire XSS story. Keep it.
- The interface has **no framework and no state container**. `model/document.ts`
  is the subject; `"value"` and `"shape"` are distinct events on purpose — a
  value change must NOT redraw the form or the caret jumps every keystroke.
  Saving is serialised (no timer, one in flight); drawing is debounced.
- `model/api.ts` is the **only** place Go and TypeScript agree on wire shapes.
  Rename a field in Go and update it there, or readers get `undefined`.
- TypeScript is strict with `noUncheckedIndexedAccess` and `types: []` (no Node
  globals); imports carry the `.ts` extension.
- Configuration is YAML (`PICVERT_CONFIG`), deliberately — the example file is
  three-quarters comments. Env vars are `PICVERT_*`: `HOME`, `DATA`, `ADDR`,
  `ADMIN_ADDR`, `PUBLIC`, `TEMPLATE`, and more in `deploy/picvert.env.example`.
- User-facing copy in templates and samples is French.
- `data/`, `*.html`/`*.pdf`/`*.png` artefacts and `picvert.yaml` are gitignored;
  `internal/server/web/*.html` is explicitly un-ignored because it is source.

## Testing

Plain `func TestX(t *testing.T)` throughout (~190 tests), a few using `t.Run`
subtests; no external test framework. Tests that matter and are easy to miss:

- `internal/templates/conformance_test.go` — every template, automatically.
- `internal/server/standalone_test.go` — runs the engine with `PICVERT_HOME`
  pointing at an empty directory, because "it is one self-contained binary" was
  claimed in a dozen places and was false.
- `internal/layout/wrap_test.go` + `testdata/wrapping.json` — line breaking
  against real Chrome measurements; read `testdata/README.md` before touching
  it (`canvas.measureText` does not load fonts; headless Chrome quantises
  advances without `--font-render-hinting=none`).
- `internal/pdf/painter_test.go` — replays the content stream and measures where
  ink actually lands, not what the text extracts as.
- `internal/server/{concurrency,memory,cost,lease,conflict}_test.go` — the
  service's performance and correctness claims; these are the ones that fail on
  a contended CI core.

The interface has no automated behavioural tests; it is driven by hand over the
debugging protocol. Types catch shape errors only.
