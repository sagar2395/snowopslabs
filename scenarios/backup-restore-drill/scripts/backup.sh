#!/usr/bin/env bash
set -euo pipefail

# backup.sh <namespace> — capture a namespace's resources into a restorable
# manifest archive (JSON). Server-managed fields (status, uid, resourceVersion,
# managedFields, …) are stripped so the archive re-applies cleanly into the same
# or a fresh namespace.
#
# Manifest-level backup: this round-trips Kubernetes objects, NOT PersistentVolume
# data. For stateful data use a volume snapshot or Velero with restic.
#
# Env:
#   BACKUP_DIR  where archives are written (default: .labctl/backups)
#   RESOURCES   comma-separated kinds to capture (default: a sensible app set)

NS="${1:?Usage: backup.sh <namespace>}"
BACKUP_DIR="${BACKUP_DIR:-.labctl/backups}"
RESOURCES="${RESOURCES:-deployment,service,configmap,secret,serviceaccount,ingress,pdb,horizontalpodautoscaler}"

if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: 'jq' is required to scrub manifests but was not found." >&2
  echo "       Install it: brew install jq  (macOS) | apt-get install jq (Debian/Ubuntu)" >&2
  exit 1
fi

if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
  echo "ERROR: namespace '${NS}' not found." >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"
TIMESTAMP=$(date -u +"%Y%m%dT%H%M%SZ")
ARCHIVE="${BACKUP_DIR}/${NS}-${TIMESTAMP}.json"
LATEST="${BACKUP_DIR}/${NS}-latest.json"

echo "==> Backing up namespace '${NS}' (${RESOURCES})"

# Capture, then scrub. We drop:
#   - server-managed metadata (uid, resourceVersion, generation, timestamps, managedFields)
#   - the whole status subresource
#   - the last-applied-configuration annotation (noise)
#   - Service spec.clusterIP(s) so the restore gets a fresh allocation
# and we skip objects that controllers recreate or that are environment-bound:
#   - the default ServiceAccount, its token Secrets, and Helm release Secrets.
kubectl get "$RESOURCES" -n "$NS" -o json 2>/dev/null |
  jq '
      .items
      | map(select(
            (.kind == "ServiceAccount" and .metadata.name == "default") or
            (.kind == "Secret" and (.type == "kubernetes.io/service-account-token")) or
            (.kind == "Secret" and (.type == "helm.sh/release.v1"))
          | not))
      | map(
          del(
            .metadata.uid,
            .metadata.resourceVersion,
            .metadata.generation,
            .metadata.creationTimestamp,
            .metadata.managedFields,
            .metadata.annotations."kubectl.kubernetes.io/last-applied-configuration",
            .metadata.annotations."deployment.kubernetes.io/revision",
            .status
          )
          | if .kind == "Service" then del(.spec.clusterIP, .spec.clusterIPs) else . end
        )
      | {apiVersion: "v1", kind: "List", items: .}
    ' >"$ARCHIVE"

COUNT=$(jq '.items | length' "$ARCHIVE")
cp "$ARCHIVE" "$LATEST"

echo "Backed up ${COUNT} object(s) to:"
echo "  ${ARCHIVE}"
echo "  ${LATEST}  (latest pointer)"
echo ""
echo "Restore with: bash scenarios/backup-restore-drill/scripts/restore.sh ${NS}"
