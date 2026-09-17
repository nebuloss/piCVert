<div align="center">

<img src="assets/picvert.svg" width="120" alt="">

# piCVert

**A CV engine that lays the page out once.**

<sub>*pic vert* — the green woodpecker. It works one upright surface, taps at an exact spot, and never wanders off it.</sub>

</div>

---

## The problem it exists to solve

A CV is one page. Not "about a page": `794×1123` px with `overflow:hidden`, where
anything past the edge is cut off in silence.

The usual way to build one is to lay it out twice — the browser does it from
CSS for the screen, and a PDF library does it again for the file you send. Both
are given the same design, and they agree about it. They just don't agree about
where paragraphs break.

Measured on a real CV, on the same four blocks:

| block | browser | PDF engine | difference |
|---|---:|---:|---:|
| Profile | 129.0 | 124.5 | +4.5 |
| Experience | 333.2 | 306.0 | **+27.2** |
| Education | 214.5 | 224.5 | **−10.0** |
| Projects | 156.5 | 149.3 | +7.2 |
| | **833.2** | **804.3** | **+28.9** |

Note the signs change. It is not a scale factor or a padding mistake — the boxes
agree. The two engines wrap the same sentences into a different number of lines.

Twenty-nine pixels is about one line. The CV fitted its page by less than that.
So the editor reported **fits** from one engine while the other quietly cut the
last section off the bottom — the measurement said yes and the page said no.

## What it does instead

```
cv.json ──► LAYOUT ENGINE ──► frame: boxes + already-broken lines
                                   ├──► HTML painter
                                   └──► PDF painter  (+ the cv.json inside it)
```

The layout is computed **once**. Both emitters receive the same frame, with the
line breaks already decided. The HTML emitter places each measured line at the
baseline it was measured for, so the browser is given no wrapping decision to
make and cannot reach a different answer.

The usual objection to pre-broken text — that it cannot reflow — does not apply
here. The page is a fixed box that never reflows. This is the rare layout where
baking the lines in is not a compromise.

## Measuring is the whole problem

Laying boxes out is arithmetic. Deciding where a paragraph breaks depends on the
exact advance of every glyph and the kerning of every pair, so widths come from
the font file itself — the same file the page embeds and the PDF will embed.
Nothing estimates.

Checked against a real browser on real sentences: **41 of 42 paragraphs break
exactly where Chrome breaks them.** The remaining one takes an extra line.

That asymmetry is deliberate. Chrome truncates the font size to 1/64 px before
scaling — asked for 10.6 px it draws at `678/64 = 10.59375`, which is checkable:
`1086/2048 × 10.59375 = 5.6180`, exactly what it reports for `e`, against this
engine's exact `5.6209`. Reproducing that would mean matching one browser at the
expense of Firefox, Safari and the PDF. So the rule is one-sided: **this engine
may measure text as wider than it will be drawn, never narrower.** A line it says
fits always fits, and the fit check errs towards asking you to shorten a CV that
would have held — an annoyance, where the opposite is a CV cut off in silence.

## A theme is data

A template is a directory. Adding a layout means adding a folder — no Go, no
rebuild.

```
templates/material-you/
  template.json   identity, regions, accepted sections, fonts
  theme.json      palette, named styles, composition
  icons.json      glyph set
  cover.svg       picker illustration
```

`theme.json` states the palette once, the styles once, and how a document
becomes a page:

```json
"items": {
  "style": "card",
  "children": [
    { "repeat": "items", "children": [
      { "style": "itRole",   "text": "{role}" },
      { "style": "itPeriod", "when": "period", "text": "{period}" }
    ]}
  ]
}
```

Three additions to a plain node tree, and only three: `repeat` iterates a list,
`when` draws something only if a field has a value, `{field}` binds one. A
composition language that grows keywords becomes a programming language, and
then a theme is code again with worse tools.

Both the style vocabulary and the node kinds are **closed**, for the reason the
field vocabulary is: each property has exactly one layout meaning, one CSS
declaration and one PDF operator. A theme that could invent a property would be
one the engine cannot lay out and one of the emitters cannot draw — which is the
class of divergence this project exists to remove.

## Building it

```bash
go build -o picvert ./cmd/picvert        # that is the whole build
```

The templates, the fonts and the interface are compiled into the binary, so a
clone plus the Go toolchain is everything. The interface is TypeScript, bundled
by esbuild — which is written in Go — so no part of building this needs Node.

Node is needed for one thing, and it is not building: `npm run typecheck` runs
`tsc`, because esbuild strips types without checking them. CI runs it, along
with a step that rebuilds the committed bundle and fails if it differs from
what was pushed.

```bash
go generate ./...     # rebuild the interface after changing internal/server/ui
npm run typecheck     # and check that it still type-checks
```

## Use

```bash
go build -o picvert ./cmd/picvert

./picvert fit     --profile examples/jean-dupont
./picvert render  --profile examples/jean-dupont --out cv.html
./picvert pdf     --profile examples/jean-dupont --out cv.pdf
./picvert preview --profile examples/jean-dupont        # / and /cv.pdf
./picvert serve                                          # the whole service
```

```
$ ./picvert fit --profile examples/jean-dupont
fits — spacing 140%
  left     +146.7 px left
  right    +220.5 px left
```

When it does not fit, it names what to shorten rather than quoting a number you
cannot act on:

```
DOES NOT FIT, even set as tightly as this engine will go
  right      -8.0 px left
  shorten: projets-techniques
```

## The engine chooses the spacing

A design drawn at one fixed rhythm meets a single page in only two ways: it runs
over, and you are told to cut a sentence, or it stops short, and the page ends in
a band of white that says the document ran out rather than that it finished.
Both hand the design's problem to whoever wrote the text.

So the engine searches for the spacing that brings the columns down to the foot
of the page **together** — opening the gaps up on a short CV as readily as
closing them on a long one. Each column is set separately, because one rhythm
cannot fill two columns holding different amounts of text; then every column is
asked to stop on the same line, because a column ending eleven pixels above its
neighbour is the ragged edge a reader sees as unfinished even though neither is
close to overflowing.

```
each column to the page foot:    left  +1.3 px    right +11.6 px
the columns level with each other: left +11.9 px  right +11.6 px
```

What gives, and in what order: gaps, margins and padding first, always; type size
only when spacing alone cannot bring the page home, because a CV set at 92 % looks
like a CV that was shrunk and everyone recognises it. Line pitch moves only with
the type. Fixed proportions — the portrait, the dots, the gauges, the corner radii
— are never scaled at all, since that is what makes a page look squashed rather
than merely close-set. And a page cannot "fit" by running its lines into one
another: touching text disqualifies a setting outright.

The setting is reported, never hidden. A CV that only holds at the floor is a CV
that is too long, and its author is owed that fact even though the page in front
of them looks fine.

## A service, not just a renderer

```bash
sudo ./deploy/install.sh        # builds, installs, starts
picvert new --slug jean --name "Jean Dupont"
```

```
:3000  the CVs, the private links, the editor
:3001  administration — no access control, never proxied
```

Nothing is readable at a guessable address unless it is named in
`PICVERT_PUBLIC`. Everything else is reached through one of **two stable links
per CV** — one that views, one that also edits — and there is no account and no
password anywhere: on a public service, a password-protected surface would be the
only thing here actually worth attacking.

The editor generates its forms from the same field tree the validator walks and
the journal names fields by, so adding a field to the vocabulary adds it to the
interface with no interface code changing. It previews the **unsaved** document
on every pause in typing, which is the hot path of the whole service and the
reason the layout is one pass rather than two.

The journal keeps **one entry per episode of editing, not per save**: a paragraph
rewritten over two minutes is one act, and recording it as eighty lines of
"Summary → Summar → Summa…" would bury the one change you are looking for. A field
typed into and put back as it was leaves nothing behind at all.

Deleting is one click by whoever holds the link, and what it destroys exists
nowhere else — no account to recover it from, no copy on a server. So it is not
destroyed: the CV is set aside for a grace period, and the admin port can put it
back exactly as it was.

The administration port has **no access control by design**. Its protection is
topological — it is not proxied outwards — and it must stay that way, for the
same reason there is no password on the public side.

Nothing is built ahead of time. `cv.json` is the source of truth and the page is
drawn from it on request, so the class of fault where a CV is edited and the
world keeps reading the previous one does not exist here.

## The PDF carries its own source

A PDF cannot be read back into a CV: its content stream says where ink goes, not
which field a run of text came from. So the file carries the `cv.json` it was
rendered from, the way a Factur-X invoice carries its XML — about **1 %** of the
file, and the export becomes both the thing you send and a thing this engine can
open again.

Fonts are embedded and subset, with `ToUnicode` maps, so the text selects and
searches as text rather than as a picture of it.

## The page is self-contained

Styles inlined, portrait as a data URI, **fonts embedded**, no network request at
all. The fonts are not decoration: the layout was computed from those exact
files, and drawing it in whatever the reader happens to have installed would
move every break away from where it was measured. Subsetted and woff2-compressed,
the four faces cost 52 KB — a sixth of the portrait already in the file.

## How it is put together

One layout engine, many outputs. Emitters implement a `Painter` and receive the
finished frame; the traversal lives in one place, because that is where the
subtle work is and every emitter that re-derived it would be a chance to derive
it differently.

Each display kind is a `Layouter` registered by name — so `row` and `row-wrap`
share one type, being one algorithm with one flag, and a kind in the vocabulary
with no implementation fails at startup rather than measuring as nothing.

Which patterns are used where, and which are deliberately absent and why:
[`docs/DESIGN.md`](docs/DESIGN.md).

## Every template is tested by existing

The conformance kit walks `templates/` and puts each one through the same
battery, against a document synthesised from the field tree. A new template is
covered from the moment its directory exists — a kit that had to be extended per
template is one nobody extends, and the second template ships untested.

It holds four contracts that all break *silently*: a section type accepted but
not composed renders a blank card; a block the theme does not name cannot be
reported as overflowing; a page that does not read as `[header…, body]` makes the
margin measurement go quiet; and a font declared but not shipped means the page
draws in whatever the reader has — which is wider, on a layout that fits by less
than a line.

## State

Working: the layout engine, text measurement and breaking, both painters, the
embedded source, themes-as-data, the template registry, validation, the
conformance kit, mobile scaling, per-column spacing, and the service around it —
viewer, private links, editor, journal, administration.

Known gaps and deliberate differences: [`docs/STATUS.md`](docs/STATUS.md).
Deploying it: [`docs/HOSTING.md`](docs/HOSTING.md).

## Licence

MIT. The embedded Roboto faces are Apache 2.0; they travel with the engine, and
so does their notice.
