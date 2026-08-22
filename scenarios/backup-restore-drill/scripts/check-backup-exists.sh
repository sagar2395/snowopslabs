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

echo "OK: backup archive present (${LATEST})."
