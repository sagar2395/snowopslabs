#!/usr/bin/env bash
set -euo pipefail

# restore.sh <namespace> [archive] — restore a namespace from a manifest archive
# produced by backup.sh. Recreates the namespace if it was deleted, then applies
# every captured object. Idempotent: re-running over a healthy namespace is a
# no-op apply.
#
# Env:
#   BACKUP_DIR  where archives live (default: .labctl/backups)

NS="${1:?Usage: restore.sh <namespace> [archive.json]}"
BACKUP_DIR="${BACKUP_DIR:-.labctl/backups}"
ARCHIVE="${2:-${BACKUP_DIR}/${NS}-latest.json}"

if [ ! -f "$ARCHIVE" ]; then
  echo "ERROR: backup archive not found: ${ARCHIVE}" >&2
  echo "       Create one first: bash scenarios/backup-restore-drill/scripts/backup.sh ${NS}" >&2
  exit 1
fi

# Recreate the namespace if the loss simulation deleted it.
if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
  echo "==> Namespace '${NS}' is gone — recreating it"
  kubectl create namespace "$NS"
fi

echo "==> Restoring '${NS}' from ${ARCHIVE}"
kubectl apply -n "$NS" -f "$ARCHIVE"

echo ""
echo "Restore complete. Verify the round-trip:"
echo "  labctl scenario verify backup-restore-drill"
