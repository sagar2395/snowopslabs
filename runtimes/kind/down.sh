#!/usr/bin/env bash
set -euo pipefail

# Delete a kind cluster. Idempotent.

CLUSTER_NAME="${1:-${CLUSTER_NAME:-snowops}}"

if ! command -v kind >/dev/null 2>&1; then
  echo "ERROR: 'kind' is required but not installed." >&2
  exit 1
fi

if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "Cluster '$CLUSTER_NAME' not found, nothing to do."
  exit 0
fi

echo "Deleting kind cluster '$CLUSTER_NAME'..."
kind delete cluster --name "$CLUSTER_NAME"
echo "Cluster '$CLUSTER_NAME' deleted."
