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
- **`cmd/picvert`** — `render`, `pdf`, `fit`, `preview`.

## Not yet

**The service around the engine.** Profiles, storage, private links, the editor,
the HTTP surface: none of that is here. This repository is the rendering engine,
and `picvert preview` is a way to look at it, not a server.

**Arcs in SVG icon paths.** The translator handles move, line, cubic and close —
what a material icon is made of. An icon set using `A` would draw nothing rather
than something wrong; the conformance kit would not catch it, because it checks
that icons are *declared*, not that every path command is understood.

**SVG images.** A PDF cannot carry one wholesale, so a profile whose photo is an
SVG — the shipped example's is — gets a PDF with no portrait. PNG and JPEG work.

## Deliberate differences

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
- **An unmatched `q` in a PDF is not an error.** It leaves the graphics state
  pushed, so a clip applies to everything drawn afterwards. No reader complains;
  the page simply comes out mostly missing.
- **A fixed body height cannot shrink below the viewport.** On a phone that left
  a screenful of white under the CV — visible only at a width narrow enough for
  the scaling to apply, which no desktop check reaches.
- **`canvas.measureText` does not load fonts.** A reference measured that way is
  measured in a fallback and looks perfectly self-consistent. And headless Chrome
  quantises advances to whole pixels without `--font-render-hinting=none`.
- **Ask before inventing.** Two rounds were spent adding, then removing,
  ornament that was never in the design. Reading the original stylesheet — which
  should have come first — then turned up a dozen real differences in one pass.

## Next

1. **A golden-file test for the PDF**, comparing a rendered page against a
   checked-in raster rather than against my reading of a screenshot.
2. **Arc support** in the SVG path translator, so an icon set outside the
   material family renders.
3. **The browser type bridge**, if this engine is ever put back under the
   TypeScript editor: `internal/fields` and the editor's `fields.ts` would then
   be two copies with nothing holding them together.
