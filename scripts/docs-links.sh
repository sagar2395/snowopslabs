#!/bin/sh
# Fails when a relative Markdown link points at a file that does not exist.
# A broken link in the docs is a broken page on the website, so it is a build
# failure here rather than a bug report from a reader.
set -eu

root=$(unset CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

fail=0
files=$(git ls-files '*.md' | grep -v '^src/ui/node_modules/')

for f in $files; do
  dir=$(dirname "$f")
  # Pull the target out of every ](...) link, then keep only relative ones.
  targets=$(sed -n 's/.*](\([^)]*\)).*/\1/p' "$f" |
            grep -v '^https\{0,1\}:' |
            grep -v '^#' |
            grep -v '^mailto:' || true)
  for t in $targets; do
    # Strip any anchor; a bare anchor was already filtered out above.
    path=${t%%#*}
    [ -z "$path" ] && continue
    if [ ! -e "$dir/$path" ]; then
      echo "$f: broken link -> $t"
      fail=1
    fi
  done

  # Also catch `docs/...md` written in backticks. Prose cites paths that way,
  # and a rename leaves them dangling with no link syntax to flag it. ADRs are
  # exempt: they record history, including files a decision deleted.
  case "$f" in docs/adr/*) continue ;; esac

  # shellcheck disable=SC2016  # the backticks are literal Markdown, not a subshell
  cited=$(sed -n 's/.*`\(docs\/[A-Za-z0-9._/-]*\.md\)`.*/\1/p' "$f" || true)
  for c in $cited; do
    if [ ! -e "$c" ]; then
      echo "$f: cites a missing file -> $c"
      fail=1
    fi
  done
done

if [ "$fail" -ne 0 ]; then
  echo
  echo "Broken documentation links. Fix the link, or the file it should point at."
  exit 1
fi

echo "docs-links: every relative link resolves"
