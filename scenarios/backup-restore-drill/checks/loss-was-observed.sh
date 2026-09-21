#!/usr/bin/env bash
# Passes once the restore marker has been seen missing at least once.
#
# A drill has to be performed, not merely survived. Live state cannot tell
# "restored the marker" from "never deleted it" — both look identical — so the
# loss has to be witnessed while it is happening. Every verify that finds the
# marker gone records it here, which is why the drill asks the learner to verify
# straight after the delete: that run is the evidence.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_backup_lib.sh
. "${SCRIPT_DIR}/../scripts/_backup_lib.sh"

NS="$(backup_ns "")"
LOG="$(loss_log "$NS")"

if ! kubectl -n "$NS" get configmap restore-marker >/dev/null 2>&1; then
  mkdir -p "$(dirname "$LOG")"
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) restore-marker missing from ${NS}" >>"$LOG"
  echo "Loss confirmed and recorded: restore-marker is gone from ${NS}." >&2
  echo "  That is the drill working. Now restore it from the archive." >&2
  exit 1
fi

if [ -s "$LOG" ]; then
  echo "OK: the marker was observed missing $(grep -c . "$LOG") time(s) — the loss was real, not simulated on paper."
  exit 0
fi

echo "FAIL: the restore marker has never been lost, so no restore has been rehearsed." >&2
echo "  Delete it, then re-verify so the loss is witnessed:" >&2
echo "    kubectl -n ${NS} delete configmap restore-marker" >&2
echo "    labctl scenario verify backup-restore-drill" >&2
exit 1
