# State of piCVert

What works, what does not, and what is deliberately different from the engine
this was extracted from.

## Verified

```
gofmt -l .     nothing
go vet ./...   nothing
go test ./...  green
```

| | |
|---|---|
| Line breaking vs a real browser | **41/42 paragraphs identical**, the rest one line more (the safe direction) |
| PDF vs the previous engine's | same page, same layout, 293 KB against 241 KB |
| Text in the PDF | selectable and searchable, 3 265 chars extracted against 3 279 |
| Conformance kit | both templates, and it fails when a template is broken on purpose |
| Mobile | scales to fit, no horizontal scroll, no dead space below the page |
| The service | driven over HTTP by `internal/server`: publication rule, link scope, read-only links, validation, unknown properties, preview, journal, trash, link rotation |

The machine this was written on has neither `go` nor `node`; everything is
verified on a build host over SSH.

## Working

- **`internal/layout`** — the engine. Closed style vocabulary, node tree, font
  metrics read from the file (kerning included), greedy line breaking, a flexbox
  subset, and the `Painter` interface both outputs implement.
- **`internal/pdf`** — the PDF painter, written by hand. Composite fonts with
  `ToUnicode` maps, subset and embedded; images with soft masks; gradients as
  banded fills; the source `cv.json` attached the way Factur-X carries its XML.
- **`internal/theme`** — a template's palette, named styles and composition, all
  as data. `repeat`, `when`, `{field}`, `styleBy`, `widthFrom`, and nothing else.
- **`internal/templates`** — discovery, validation, and the conformance kit.
- **`internal/document`, `validate`, `fields`** — the document contract.
- **`internal/engine`** — the pipeline, start to finish, assembled in ONE place so
  what `fit` measures is what `render` draws and what `serve` sends.
- **`internal/profiles`, `store`, `tokens`, `access`, `security`, `diff`** — the
  service's data layer: where CVs live, the single door every write goes
  through, the two stable links per CV, the publication rule, the headers and
  the per-address throttle, and the document comparison the journal is built on.
- **`internal/server`** — the public port (viewer, private links, editor, API)
  and the admin port. Its browser assets are embedded in the binary.
- **`cmd/picvert`** — `render`, `pdf`, `fit`, `preview`, `serve`.

## Not yet

**Importing and exporting a CV as a file.** The PDF carries its own `cv.json`,
but nothing yet reads it back out, and there is no bundle format for a CV plus
its portrait.

**Anything self-service.** There is no route that creates a CV: profiles are
directories someone puts there. A public service taking new CVs from strangers
needs a quota, a rate limit per address and a challenge, and none of that is
written.

**Arcs in SVG icon paths.** The translator handles move, line, cubic and close —
what a material icon is made of. An icon set using `A` would draw nothing rather
than something wrong; the conformance kit would not catch it, because it checks
that icons are *declared*, not that every path command is understood.

**SVG images.** A PDF cannot carry one wholesale, so a profile whose photo is an
SVG — the shipped example's is — gets a PDF with no portrait. PNG and JPEG work.

## Deliberate differences

1. **Nothing is built ahead of time.** The engine this came from wrote
   `build/<slug>/cv.html` on every save and served that. A page drawn on request
   cannot be stale, and there is no build to roll back when one fails — the
   rendering is cached against the document's own timestamp instead.
1. **Templates are directories, compositions are data.** The engine this came
   from shipped two renderers per template, in two languages, describing one
   design twice. They drifted, and the drift is what cut CVs off the page. One
   declarative composition cannot drift from itself.
2. **The style vocabulary is closed.** Same reasoning as the field vocabulary:
   every property has one layout meaning, one CSS declaration and one PDF
   operator, so neither painter can be missing a feature the other has. The one
   exception is `shadow`, documented at the field: it is ink around a shape
   rather than part of one, changes no size or position, and the original
   already drew it on screen and left it out of the PDF.
3. **Decorations are nodes.** The timeline dots and list bullets were CSS
   pseudo-elements on one side and views on the other. As nodes, the layout
   engine knows they exist — which the CSS version never did.
4. **Measurement is one-sided.** This engine may measure text as wider than it
   will be drawn, never narrower. See the README for why, and
   `internal/layout/testdata/README.md` for the two measurement traps that
   caught me while establishing it.
5. **A document travels as `map[string]any`.** Unknown properties survive a
   read/write round trip, so a CV written by a newer version still opens in an
   older one. Go type assertions match the *exact dynamic type*, so everything
   goes through `document.AsObject` — a named map type does not satisfy
   `.(map[string]any)`, which cost an afternoon once.
6. **The page's size is the engine's, not the theme's.** A block sizes itself to
   its content, so a page left to itself simply grew to fit whatever was put on
   it: nothing overflowed and the fit check had nothing to report. A4 is 794×1123
   because that is what A4 is.

## Lessons this codebase paid for

Recorded because each cost real time and none is obvious from the code.

- **A root exempt from the painting rules is a root whose style nobody checks.**
  The page was written by hand and only its children painted, so its background
  silently vanished — and every card being white too, a whole column dissolved
  into the backdrop.
- **A subsetter renumbers glyphs.** Encoding text against one font file and
  embedding another produces a page of plausible gibberish, with the metrics
  still correct because the widths came from the same wrong indices.
- **PDF text state outlives the text object that set it.** `BT`/`ET` resets the
  text matrix and nothing else, so `Tc` — character spacing — written only when
  non-zero leaked out of every section title into the paragraphs after it. At
  0.6 pt a character, a thirty-character run came out twenty points wider than
  it was measured and was drawn over the run beside it. Nothing that READS a
  PDF can see this: the text extracts perfectly, because extraction does not
  care where the glyphs landed.
- **An unmatched `q` in a PDF is not an error.** It leaves the graphics state
  pushed, so a clip applies to everything drawn afterwards. No reader complains;
  the page simply comes out mostly missing.
- **A fixed body height cannot shrink below the viewport.** On a phone that left
  a screenful of white under the CV — visible only at a width narrow enough for
  the scaling to apply, which no desktop check reaches.
- **`canvas.measureText` does not load fonts.** A reference measured that way is
  measured in a fallback and looks perfectly self-consistent. And headless Chrome
  quantises advances to whole pixels without `--font-render-hinting=none`.
- **A renderer that reads a map is a renderer that cannot be compared to
  itself.** A style asking for a font weight nobody shipped was resolved by
  whichever face the map happened to yield first, and Go randomises that
  deliberately. The same CV then laid out to a different page from one run to
  the next — and the fit search, which lays a page out ten times, turned the
  wobble into visibly different spacing. Every fallback order is now fixed.
- **Ask before inventing.** Two rounds were spent adding, then removing,
  ornament that was never in the design. Reading the original stylesheet — which
  should have come first — then turned up a dozen real differences in one pass.

## Next

1. **A golden-file test for the PDF**, comparing a rendered page against a
   checked-in raster rather than against my reading of a screenshot.
2. **Arc support** in the SVG path translator, so an icon set outside the
   material family renders.
3. **Self-service**, if this is ever opened to strangers: quota, per-address
   creation limit, challenge.
