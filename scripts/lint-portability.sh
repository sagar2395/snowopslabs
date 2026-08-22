#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Portability gate (golden rule 1): reject GNU-only constructs that work on
# Linux and silently fail — or worse, silently misbehave — on macOS's BSD
# userland. shellcheck does not catch these because they are valid syntax.
#
# Comment-only lines are skipped, so documenting a pitfall does not trip the
# gate that enforces it. A file that must contain these constructs as data —
# only this gate's own test fixtures, realistically — opts out with the marker
# `portability-gate: ignore-file` anywhere in it.
#
# Run by `make lint-shell` and by CI on every PR.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

# Rules are <scope><TAB><extended-regex><TAB><explanation>.
#   scope "sh"  — shell scripts only (Makefiles legitimately name build targets)
#   scope "all" — every scanned file
rules=$(
  cat <<'RULES'
all	grep[[:space:]]+-[a-zA-Z]*P[[:space:]]	grep -P (PCRE) is GNU-only; use grep -E or awk
all	sed[[:space:]]+-i[[:space:]]+[^.]	sed -i without a backup suffix differs on BSD; use sed -i.bak (and remove the .bak) or a temp file
all	readlink[[:space:]]+-f	readlink -f is GNU-only; use a cd/pwd subshell
all	date[[:space:]]+-d[[:space:]]	date -d is GNU-only; compute the value in Go instead
all	stat[[:space:]]+-c[[:space:]]	stat -c is GNU-only; BSD uses stat -f
all	base64[[:space:]]+-w	base64 -w is GNU-only; BSD base64 does not wrap
all	xargs[[:space:]]+-r	xargs -r is GNU-only; guard with a test, or use xargs with a no-op default
all	mktemp[[:space:]]+-t[[:space:]]+[^ ]+[[:space:]]*$	bare mktemp -t behaves differently on BSD; pass a full XXXXXX template
sh	linux/amd64	hardcoded linux/amd64 breaks Apple Silicon; detect OS/arch
RULES
)

collect() {
  find . \
    \( -path ./.git -o -path ./node_modules -o -path ./ui/node_modules \
    -o -path ./docs/archive -o -path ./.ai/archive \
    -o -name 'lint-portability.sh' \) -prune -o \
    \( "$@" \) -print |
    while IFS= read -r f; do
      grep -q 'portability-gate: ignore-file' "$f" 2>/dev/null || printf '%s\n' "$f"
    done
}

sh_files=$(collect -name '*.sh' -o -name '*.bats')
all_files=$(collect -name '*.sh' -o -name '*.bats' -o -name '*.mk' -o -name 'Makefile')

status=0
while IFS="$(printf '\t')" read -r scope pattern explanation; do
  [ -n "${pattern:-}" ] || continue

  case "$scope" in
    sh) files="$sh_files" ;;
    *) files="$all_files" ;;
  esac

  # -H forces the filename prefix even when only one file is scanned, which
  # both keeps violations locatable and lets the comment filter below work.
  hits=$(
    printf '%s\n' $files |
      xargs grep -HnE "$pattern" 2>/dev/null |
      grep -vE '^[^:]+:[0-9]+:[[:space:]]*#' || true
  )

  if [ -n "$hits" ]; then
    printf '\n%s\n' "$explanation" >&2
    printf '%s\n' "$hits" >&2
    status=1
  fi
done <<EOF
$rules
EOF

if [ "$status" -ne 0 ]; then
  printf '\nportability gate failed — see CONTRIBUTING.md golden rules 1\n' >&2
  exit 1
fi

echo "portability gate passed"
