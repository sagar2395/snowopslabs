#!/usr/bin/env bash
set -euo pipefail

# check-backup-exists.sh — exit 0 only if a backup archive exists for the target
# namespace. A restore drill is only meaningful if a backup was actually taken;
# this is the "you cannot restore what you never captured" guardrail.
#
# Env:
#   BACKUP_DIR  where archives live (default: .labctl/backups)
#   NAMESPACE   namespace to check a backup for (default: go-api)

BACKUP_DIR="${BACKUP_DIR:-.labctl/backups}"
NAMESPACE="${NAMESPACE:-go-api}"
LATEST="${BACKUP_DIR}/${NAMESPACE}-latest.json"

if [ ! -f "$LATEST" ]; then
  echo "FAIL: no backup archive found at ${LATEST}" >&2
  echo "Run: bash scenarios/backup-restore-drill/scripts/backup.sh ${NAMESPACE}" >&2
  exit 1
fi

# Surface the backup's state — how many objects it holds and how old it is — so
# a passing check tells the operator *what* they would restore, not just that a
# file exists. Age uses `date -r <file>` (unlike `stat`, its flags agree
# between BSD and GNU) and is best-effort.
COUNT="?"
if command -v jq >/dev/null 2>&1; then
  COUNT=$(jq '.items | length' "$LATEST" 2>/dev/null || echo "?")
fi
MTIME=$(date -r "$LATEST" +%s 2>/dev/null || echo "")
AGE=""
if [ -n "$MTIME" ]; then
  NOW=$(date -u +%s)
  SECS=$((NOW - MTIME))
  if [ "$SECS" -lt 90 ]; then
    AGE=", taken ${SECS}s ago"
  else
    AGE=", taken $((SECS / 60))m ago"
  fi
fi

echo "OK: backup archive present — ${COUNT} object(s)${AGE} (${LATEST})."
