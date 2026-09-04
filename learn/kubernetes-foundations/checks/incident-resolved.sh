#!/usr/bin/env bash
# Passes when the learner has injected service-selector-broken and fixed it
# themselves. Cluster state after a resolve is identical to state before the
# inject, so the run history is the only evidence the module was actually done
# — and resolvedBy distinguishes a real fix from the escape hatch.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
HISTORY="$ROOT/.labctl/history/results.jsonl"

[ -f "$HISTORY" ] || exit 1

grep '"kind":"incident"' "$HISTORY" |
  grep '"name":"service-selector-broken"' |
  grep -q '"resolvedBy":"manual"'
