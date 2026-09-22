#!/usr/bin/env python3
"""Find text drawn on top of text, from the PDF's own character boxes.

    mutool draw -F stext -o out.stext out.pdf
    python3 scripts/pdf-overlap.py out.stext

Prints the number of overlapping character pairs and the worst few in context.
Zero is the only acceptable answer.

# WHY THIS EXISTS RATHER THAN A TEXT CHECK

A PDF whose runs are drawn on top of one another extracts PERFECTLY: the text
is all there, in order, with its spaces. Only the geometry is wrong. So this
whole class of fault is invisible to anything that reads a PDF's text, which
is what every existing check did — and a real overlap of up to 4.2 pt shipped
in a release because of it.

The threshold is 0.6 pt rather than zero: adjacent glyph boxes legitimately
touch and sometimes nudge into one another by a fraction, and a check that
fires on that is a check nobody trusts.
"""
import re, sys

s = open(sys.argv[1], encoding="utf-8", errors="replace").read()
lines = re.findall(r"<line[^>]*>(.*?)</line>", s, re.S)
worst = []
for body in lines:
    chars = []
    for m in re.finditer(r'<char\s+quad="([^"]+)"[^>]*?\sc="([^"]*)"', body):
        v = [float(x) for x in m.group(1).split()]
        chars.append((min(v[0::2]), max(v[0::2]), m.group(2)))
    for i in range(1, len(chars)):
        prev_x1 = chars[i - 1][1]
        x0 = chars[i][0]
        # A real overlap, not the sub-pixel snugness of normal kerning.
        if x0 < prev_x1 - 0.6:
            ctx = "".join(c for _, _, c in chars[max(0, i - 14):i + 14])
            worst.append((prev_x1 - x0, ctx))
worst.sort(reverse=True)
print(f"{len(worst)} overlapping character pairs")
seen = set()
for amount, ctx in worst:
    k = ctx[:24]
    if k in seen:
        continue
    seen.add(k)
    print(f"  {amount:6.2f} pt  …{ctx}…")
    if len(seen) >= 8:
        break
