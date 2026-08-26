#!/usr/bin/env bash
set -euo pipefail

# Re-deploy go-api with deliberately over-provisioned CPU/memory requests — the
# 'before' state to observe in OpenCost. We UPGRADE the existing `go-api` Helm
# release in place rather than creating a second release: `labctl app deploy
# go-api` already owns a `go-api` release (and the `go-api` Deployment) in the
# go-api namespace, so a separate release trying to adopt the same namespace and
# Deployment fails helm's ownership validation. Reusing the release converges
# cleanly and matches the scenario's story ("re-deploy go-api over-provisioned").

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCENARIO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_ROOT="$(cd "$SCENARIO_DIR/../.." && pwd)"
CHART="$PROJECT_ROOT/apps/go-api/deploy/helm"
VALUES="$SCENARIO_DIR/values/overprovisioned.yaml"

echo "Re-deploying go-api over-provisioned (helm release: go-api)..."
# Match the canonical app deploy (src/engine/deploy/helm.sh): create the namespace
# out-of-band and pass namespace.create=false, so the chart doesn't render a
# Namespace object helm cannot adopt (the go-api namespace already exists,
# created by kubectl rather than helm).
kubectl create namespace go-api --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1 || true
helm upgrade --install go-api "$CHART" \
  --namespace go-api --create-namespace \
  --set namespace.create=false \
  -f "$VALUES" \
  --wait --timeout 5m

echo "Done. go-api now requests 2000m CPU / 1Gi memory (the inflated baseline)."
echo "Observe the cost in OpenCost, then right-size with 'kubectl -n go-api set resources'."
