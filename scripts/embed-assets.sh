#!/usr/bin/env bash
# go:embed cannot reach outside its own directory, so the templates and fonts
# are linked into internal/assets. Symlinks are NOT followed by go:embed, so
# these are real copies kept in step by `go generate`.
set -eu
cd "$(dirname "$0")/.."
rm -rf internal/assets/templates internal/assets/fonts
mkdir -p internal/assets/templates internal/assets/fonts

# The template DIRECTORIES only. Copying everything once dragged a stray .go
# file in, which go:embed happily included and the compiler then tried to build
# as part of the assets package.
for dir in templates/*/; do
  [ -f "$dir/template.json" ] || continue
  cp -r "$dir" internal/assets/templates/
done

# The .ttf the engine measures with and the PDF embeds, and the .woff2 a page
# carries. The full .ttf is NOT copied when a subset exists beside it: the
# subset is what the page embeds and what the PDF subsets further, and shipping
# both doubles the fonts in the binary for no purpose.
cp fonts/*.ttf fonts/*.woff2 internal/assets/fonts/ 2>/dev/null || true
cp fonts/README.md internal/assets/fonts/ 2>/dev/null || true

echo "  templates: $(find internal/assets/templates -type f | wc -l) files"
echo "  fonts:     $(find internal/assets/fonts -type f | wc -l) files"
