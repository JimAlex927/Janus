#!/bin/sh
set -eu

# Compare the reviewed Vite output with the tracked Go embed. This prevents a
# source-only frontend change from silently shipping an older Admin Console.
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
dist="$root/frontend/dist"
embed="$root/internal/admin/ui"

test -f "$dist/index.html"
test -f "$embed/index.html"
cmp "$dist/index.html" "$embed/index.html"

dist_assets=$(mktemp)
embed_assets=$(mktemp)
trap 'rm -f "$dist_assets" "$embed_assets"' EXIT
find "$dist/assets" -maxdepth 1 -type f -exec basename {} \; | sort > "$dist_assets"
find "$embed/assets" -maxdepth 1 -type f -exec basename {} \; | sort > "$embed_assets"
cmp "$dist_assets" "$embed_assets"

while IFS= read -r asset; do
	cmp "$dist/assets/$asset" "$embed/assets/$asset"
done < "$dist_assets"

printf 'embedded Admin Console matches frontend/dist\n'
