#!/usr/bin/env bash
set -euo pipefail

# In-cluster runtime "up" — no-op. The labctl server already runs inside the
# target cluster (team mode, task 063); there is no cluster to create. kubectl
# and helm use the in-cluster ServiceAccount config automatically.

echo "In-cluster runtime: using the hosting cluster via the ServiceAccount token."

if ! kubectl cluster-info >/dev/null 2>&1; then
  echo "WARNING: kubectl cannot reach the API server. In a pod this should" >&2
  echo "         work via the mounted ServiceAccount; check RBAC and the" >&2
  echo "         automountServiceAccountToken setting." >&2
  exit 1
fi

kubectl version -o yaml 2>/dev/null | grep -i "gitVersion" | head -2 || true
echo "In-cluster runtime ready."
