#!/bin/sh
# Fails when the website's docs manifest names a file this repo no longer has.
#
# snowopslabs-web publishes documentation by reading these files directly. A
# rename here with no matching manifest edit drops the page from the site
# silently, so the check lives in this repo, where the rename happens.
#
# Set SNOWOPSLABS_WEB to the site checkout; it defaults to a sibling directory.
# The check is skipped, not failed, when the site is not checked out.
set -eu

root=$(unset CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

web=${SNOWOPSLABS_WEB:-../snowopslabs-web}
manifest="$web/scripts/docs-manifest.mjs"

if [ ! -f "$manifest" ]; then
  echo "docs-manifest: $manifest not found — skipping (set SNOWOPSLABS_WEB to check)"
  exit 0
fi

fail=0
# Every `src: "..."` in the manifest is a path in this repo: a file, or a
# directory when the entry is globbed.
srcs=$(sed -n 's/.*src:[[:space:]]*"\([^"]*\)".*/\1/p' "$manifest")

for s in $srcs; do
  if [ ! -e "$s" ]; then
    echo "docs-manifest: $s is published by the website but does not exist here"
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo
  echo "Update scripts/docs-manifest.mjs in snowopslabs-web, then run 'npm run sync' there."
  exit 1
fi

echo "docs-manifest: every published path exists"
