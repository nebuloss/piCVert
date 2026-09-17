# Browser reference for line breaking

`wrapping.json` records how a real browser breaks paragraphs taken from a real
CV, at the widths and sizes the Material You layout actually uses. It is what
`TestLineBreaksAreNeverOptimistic` checks this engine against.

Regenerate on a machine with a headless Chromium:

```bash
./scripts/capture-wrapping.sh
```

## Two traps, both of which caught me

**Canvas does not load fonts.** `document.fonts.ready` waits only for faces the
document actually uses, and `canvas.measureText` silently falls back to a
default font rather than triggering a load. A first version of this reference
was measured entirely in the wrong font and looked plausible — all the numbers
were self-consistent. Put the face in the DOM, and `await document.fonts.load()`
explicitly, before measuring anything.

**Headless Chrome quantises advances to whole pixels** unless you pass
`--font-render-hinting=none`. Without it every glyph comes back as `5.000`,
`3.000`, `9.000`, which is not what any browser draws on a screen and makes the
reference useless.

## Why the test is one-sided

Chrome truncates the font size to 1/64 px before scaling: asked for 10.6 px it
draws at `678/64 = 10.59375`. That is checkable against the font's own metrics —
`1086/2048 × 10.59375 = 5.6180`, exactly what the browser reports for `e`,
against this engine's exact `5.6209`.

So a browser draws text about **0.06 % narrower** than this engine measures it,
and that figure belongs to one renderer: Firefox and Safari quantise
differently, and the PDF not at all. Reproducing it would mean matching Chrome
at the expense of the other two and of the PDF.

The property worth holding is therefore one-sided: **this engine may measure
text as wider than it will be drawn, never narrower.** A line it says fits then
always fits, and the fit check errs towards asking someone to shorten a CV that
would have held — an annoyance, where the opposite is a CV cut off in silence.

At the time of writing, 41 of 42 paragraphs break exactly as the browser breaks
them; the remaining one takes an extra line, which is the safe direction.
