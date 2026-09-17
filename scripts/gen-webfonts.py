#!/usr/bin/env python3
"""Generates the web fonts the standalone CV embeds.

WHY THIS EXISTS. `theme.css` asks for Roboto and, until now, shipped nothing:
the HTML relied on the viewing machine having Roboto installed. The PDF does
not — it embeds its fonts — which is why the project can promise the PDF is
identical everywhere and could promise nothing at all about the page.

That gap is not cosmetic. Measured on this layout, a typical fallback is 14 %
wider than Roboto (DejaVu Sans) and up to 25 % wider (Liberation). The CV fits
its single page by about 19 px of slack in the main column — less than one
line — so a substituted font does not merely look different: it pushes the last
section off the page, where `overflow:hidden` silently cuts it. A recruiter
without Roboto was reading a truncated CV.

The same four faces as the PDF, subsetted to the Latin repertoire a CV uses and
compressed to woff2: 1.2 MB of TTF becomes about 42 KB, which is a sixth of the
photo already embedded in the same file.

    python3 scripts/gen-webfonts.py

Run it when the fonts change, which is approximately never. The output is
committed, so no build step and no Python are needed to serve a CV.
"""
import io
import os
import sys

try:
    from fontTools import subset
    from fontTools.ttLib import TTFont
except ImportError:
    sys.exit("fontTools is required: pip install fonttools brotli")

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FONTS = os.path.join(ROOT, "fonts")

# What a CV is written in: ASCII, Latin-1 Supplement and Latin Extended-A —
# which covers French, German, Spanish, Polish, Czech and the rest of Latin
# Europe — plus the punctuation the layout itself emits (the middle dot between
# subtitle parts, the en dash in a period, typographic quotes).
CODEPOINTS = (
    list(range(0x20, 0x7F))
    + list(range(0xA0, 0x180))
    + [0x2018, 0x2019, 0x201C, 0x201D, 0x2013, 0x2014, 0x2022, 0x00B7, 0x2026, 0x20AC]
)

FACES = ["Regular", "Medium", "Bold", "Italic"]


def main():
    total = 0
    for name in FACES:
        src = os.path.join(FONTS, f"Roboto-{name}.ttf")
        if not os.path.exists(src):
            sys.exit(f"missing {src}")
        font = TTFont(src)

        options = subset.Options()
        options.notdef_outline = True
        # THE LAYOUT TABLES ARE KEPT, deliberately.
        #
        # They were dropped in the first version of this script, because they
        # are the largest remaining piece and the repertoire no longer needs
        # most of what they describe. That was a bad trade: GPOS carries the
        # KERNING, so dropping it left every “AV”, “To” and “Ta” pair spaced as
        # if the letters had never met — visible at a glance in the headings,
        # and on a document whose whole purpose is to be looked at.
        #
        # Keeping them costs 1.9 KB per face. Typography is what this file
        # exists to protect; saving eight kilobytes by spoiling it is
        # optimising the one number nobody was looking at.
        subsetter = subset.Subsetter(options=options)
        subsetter.populate(unicodes=CODEPOINTS)
        subsetter.subset(font)

        font.flavor = "woff2"
        buf = io.BytesIO()
        font.save(buf)
        out = os.path.join(FONTS, f"Roboto-{name}.woff2")
        with open(out, "wb") as fh:
            fh.write(buf.getvalue())
        size = len(buf.getvalue())
        total += size
        print(f"  Roboto-{name:<8} {size/1024:6.1f} KB")
    print(f"  total     {total/1024:6.1f} KB")


if __name__ == "__main__":
    main()
