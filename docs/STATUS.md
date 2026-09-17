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
| Real CV, both languages | lays out end to end, fit reported per region |
| Document validation | 22 tests, ported with the rule that unknown properties survive |

## Working

- **`internal/layout`** — the engine. Closed style vocabulary, node tree, font
  metrics from the file itself (kerning included), greedy line breaking, a
  flexbox subset, and the HTML emitter that places every measured line.
- **`internal/theme`** — a template's palette, named styles and composition,
  all as data. `repeat` / `when` / `{field}` and nothing else.
- **`internal/templates`** — discovery and validation of template directories.
  Refuses a template that accepts a section type it composes nothing for.
- **`internal/document`, `internal/validate`, `internal/fields`** — the document
  envelope, the field vocabulary, and validation that walks it.
- **`cmd/picvert`** — `render` and `fit`.

## Not yet

**The PDF emitter.** The frame is already the right shape for it: absolute
positions, resolved colours, lines already broken. What is missing is the
content-stream writer, the font embedding and the `/ToUnicode` maps that make
the text selectable.

When it lands it should carry the source `cv.json` as an embedded file
(PDF/A-3, the mechanism Factur-X invoices use). Measured: deflated, the document
is **1.0 %** of the PDF. That makes the exported file both the thing you send
and a valid import — without the engine ever having to parse drawing operators
back, which the previous engine tried and abandoned.

**Placement bugs on a dense CV.** Visible in a full render:

- text nodes overlap their siblings in a couple of places — the baseline
  translate interacts with the enclosing box's height;
- wrapped chips can overlap;
- the language gauge draws no fill: the composition has no way yet to bind a
  width to a percentage of its parent. That is a real gap in the composition
  language, not a bug in a theme.

**The rest of the service** — profiles, storage, private links, the editor, the
HTTP surface — is not here. This repository is the rendering engine.

## Deliberate differences

1. **Templates are directories, compositions are data.** The engine this came
   from shipped two renderers per template, in two languages, describing one
   design twice. They drifted, and the drift is what cut CVs off the page. One
   declarative composition cannot drift from itself.
2. **The style vocabulary is closed.** Same reasoning as the field vocabulary:
   every property has one layout meaning, one CSS declaration and one PDF
   operator, so neither emitter can be missing a feature the other has.
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

## Next

1. The composition gap: binding a width to a percentage (the gauge).
2. The placement bugs, against a screenshot of a dense CV.
3. The PDF emitter, then the embedded source.
4. A conformance kit: synthesise a document from the field tree and put every
   template in `templates/` through the same battery, so a new template is
   covered by existing.
