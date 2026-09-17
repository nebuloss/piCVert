# Fonts

The four Roboto faces the Material You template uses, under the **Apache
License 2.0**. They travel with the engine, and so does their notice.

Two forms of each:

- `.ttf` — what the layout engine measures with, and what a PDF embeds.
- `.woff2` — the same outlines, subsetted and compressed, for the page to embed.

Both must come from the same source file. Measuring in one font and drawing in
another moves every line break away from where it was computed, which is the one
fault this engine exists to prevent.

Regenerate the `.woff2` from the `.ttf` with:

```bash
python3 scripts/gen-webfonts.py
```

The subset keeps the Latin repertoire a CV needs, and **keeps the layout tables**
— `GPOS` carries the kerning. Dropping it saves 1.9 KB per face and leaves every
"AV", "To" and "Ta" pair spaced as if the letters had never met.
