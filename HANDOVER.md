# HANDOVER — PDF text overlap

Delete this file once the bug is fixed and the test below is in the repo.

## The bug, in one sentence

The PDF draws some runs of text about 3 % wider than the layout measured
them, so the next run — placed at the measured width — lands on top of it.

Reported by the operator as "text overlap in the PDF while the preview is
fine", and that asymmetry is exactly right.

## Reproduced, on the real CV, on the appliance

Container 405 (`dmz-picvert`, 10.0.60.211), `picvert 0.3.1`, profile
`guillaume`. Its `cv.json` is byte-identical to the copy on the build host
(`md5 330592ab6ca8d47cdc36782c1c11b49d`).

    PDF   9 overlapping character pairs, up to 4.20 pt
    HTML  119 styled runs, 0 overlaps

The worst of them, all at a regular → bold boundary:

    4.20 pt  …d'un serveur MCP reliant Gh…
    3.17 pt  …travaille à la refonte de Zee…
    2.87 pt  …du protocole SCEP pour l'au…
    2.43 pt  …d'un nouveau système de paq…

## Where it is NOT

Ruled out by measurement, not by reading:

- **The layout.** Piece 0 of the affected line is `"Conception d'un nouveau "`,
  width 110.050 px, and it does include its trailing space.
- **The placement.** The bold run is drawn at 110.051 px from the start of the
  line. That is the measured width, to a thousandth.
- **Kerning.** The first hypothesis was that the layout measures with kerning
  and `Tj` draws without it. Implemented as a `TJ` array with kern adjustments:
  overlaps went 9 → 12 and the magnitudes did not move. Reverted; the tree is
  clean at v0.3.1.

The run is placed correctly and **drawn** 113.286 px wide against 110.050
measured — a ratio of 1.0294.

## Where it is

Two code paths load different font FILES.

    internal/engine/engine.go:211   (loadFonts — what the layout MEASURES with)
        raw, err := e.Registry.ReadFont(tpl, src.File)                    // ORIGINAL

    internal/engine/engine.go:269   (PDF — what it DRAWS with)
        raw, err := e.Registry.ReadFont(p.Template, pdf.SubsetName(file)) // SUBSET

The subset renumbers glyphs, so indices and advances taken from one do not
describe the other:

    Roboto-Regular.ttf         numHMetrics=3358  advances=[908, 0, 0, 508, 508, 508]
    Roboto-Regular.subset.ttf  numHMetrics=371   advances=[908, 508, 528, 656, 1261, 1151]

HTML escapes this because the browser lays the runs out itself and never
consults the measured widths. The PDF places each run explicitly, so the
error is visible.

Two things in the code already anticipated this:

- `internal/engine/engine.go:206` — "The file measured, the file drawn and the
  file sent are one file." That invariant is what is broken.
- `internal/pdf/font.go`, on `LoadFace` — warns about drawing with a font whose
  indices came from a different file. `LoadFace` holds the invariant correctly
  and **is dead code**; the engine takes its own path and does not.

## The fix

Most likely two lines: have `loadFonts` read the subset when one exists, the
same way the PDF path does, so both sides parse identical bytes. That is also
what `font.go` says it wants — "the page and the PDF then also carry literally
the same bytes".

Check afterwards that the HTML still embeds the same face it measures with
(the page carries its fonts as data URIs) and that `internal/build`'s
byte-comparison tripwire is re-blessed if the page legitimately changes.

Consider deleting `pdf.LoadFace` or routing the engine through it, so there is
one way to build a face rather than two that can disagree.

## The acceptance test

`/tmp/overlap.py` on the build host reads character boxes straight from the
PDF and counts pairs where one character starts more than 0.6 pt before the
previous one ends. **It currently reports 9. It must report 0.**

    ssh guillaume@10.0.50.21
    cd ~/picvert
    go build -o /tmp/pv-x ./cmd/picvert
    PICVERT_HOME=$PWD /tmp/pv-x pdf --profile /tmp/realcv --out /tmp/y.pdf
    mutool draw -F stext -o /tmp/y.stext /tmp/y.pdf
    python3 /tmp/overlap.py /tmp/y.stext

`/tmp/realcv` is the real CV. /tmp there is a tmpfs and is cleared, so a copy
is kept outside it at `~/picvert-fixtures/realcv` on the build host:

    cp -a ~/picvert-fixtures/realcv /tmp/realcv

And if that is gone too, it came from:

    SRC=/home/guillaume/misc/cv/cv-repo/.claude/worktrees/cv-node-app/data/guillaume
    rsync -az "$SRC/" guillaume@10.0.50.21:/tmp/realcv/

This probe belongs in the repo as a Go test. The whole class of bug is
invisible to anything that reads a PDF's TEXT rather than its GEOMETRY: the
extracted text of the broken PDF is perfect, which is why this survived every
existing check.

## Verifying against the appliance

    ssh root@10.0.0.2
    pct exec 405 -- sh -lc "picvert pdf --profile /var/lib/picvert/data/guillaume --out /tmp/o.pdf"
    pct pull 405 /tmp/o.pdf /tmp/o.pdf

Beware: `pct exec` without `-lc` has a minimal PATH and will not find
`picvert`, which is a symlink in /usr/local/bin.

## Traps already paid for

- **The bundle.** `rsync`ing `internal/` from the build host over the local
  tree replaces freshly generated assets with stale ones, and preflight then
  fails with "the bundle is stale". Regenerate AFTER syncing, or sync only
  `internal/server/web/assets/` with `--delete`.
- **`/tmp` on the build host is a 16 GB tmpfs** shared with other projects. It
  hit 100 % during this session and silently truncated a script to zero bytes.
- **A probe that measures nothing reports zero.** The first HTML check said
  "0 overlaps" because it looked for `<b>`; the engine emits
  `<span style="font-weight:…">`. Always print the population as well as the
  failures.
- **`pkill -f picvert`** over SSH kills the session carrying it. Match the
  binary path, or use the bracket trick.

## Unrelated, still outstanding for the operator

- The Turnstile secret has been in plain text in a chat log all day and should
  be rotated.
- The appliance's administration password is `picvert-admin-2026`, set during
  testing after nested shell quoting mangled the previous one. It should be
  changed with `picvert passwd`.
