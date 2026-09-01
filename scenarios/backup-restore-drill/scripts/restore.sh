#!/usr/bin/env bash
set -euo pipefail

# restore.sh <namespace> [archive] — restore a namespace from a manifest archive
# produced by backup.sh. Recreates the namespace if it was deleted, then applies
# every captured object. Idempotent: re-running over a healthy namespace is a
# no-op apply.
#
# We restore with server-side apply (--server-side --force-conflicts) rather than
# the default client-side apply. The archive deliberately strips the
# kubectl.kubernetes.io/last-applied-configuration annotation, which client-side
# apply warns loudly about on every object ("resource … is missing the
# annotation … will be patched automatically"). Those warnings look like errors
# and erode trust in a restore that actually succeeded. Server-side apply does
# not depend on that annotation, so the restore is quiet and we print our own
# object-count summary instead.
#
# Env:
#   BACKUP_DIR  where archives live (default: .labctl/backups)

if [ $# -lt 1 ]; then
  echo "Usage: restore.sh <namespace> [archive.json]" >&2
  exit 1
fi
NS="$1"
BACKUP_DIR="${BACKUP_DIR:-.labctl/backups}"
ARCHIVE="${2:-${BACKUP_DIR}/${NS}-latest.json}"

if [ ! -f "$ARCHIVE" ]; then
  echo "ERROR: backup archive not found: ${ARCHIVE}" >&2
  echo "       Create one first: bash scenarios/backup-restore-drill/scripts/backup.sh ${NS}" >&2
  exit 1
fi

OBJECTS="unknown number of"
if command -v jq >/dev/null 2>&1; then
  OBJECTS=$(jq '.items | length' "$ARCHIVE" 2>/dev/null || echo "unknown number of")
fi

# Recreate the namespace if the loss simulation deleted it.
if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
  echo "==> Namespace '${NS}' is gone — recreating it"
  kubectl create namespace "$NS"
fi

echo "==> Restoring '${NS}' from ${ARCHIVE} (${OBJECTS} object(s))"
# Server-side apply keeps the output clean (no last-applied-configuration
# warnings) and converges cleanly whether the namespace is fresh or drifted.
kubectl apply --server-side --force-conflicts -n "$NS" -f "$ARCHIVE"

echo ""
echo "Restored ${OBJECTS} object(s) into '${NS}'."
echo "Verify the round-trip:"
echo "  labctl scenario verify backup-restore-drill"
