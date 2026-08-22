#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Installing Tempo..."

helm repo add grafana https://grafana.github.io/helm-charts --force-update
helm repo update

# Clean up stuck pending releases if any
if helm status tempo -n "$NAMESPACE" 2>/dev/null | grep -q "pending-"; then
  echo "Cleaning up stuck Tempo release..."
  helm delete tempo -n "$NAMESPACE" --wait 2>/dev/null || true
fi

helm upgrade --install tempo grafana/tempo \
  --namespace "$NAMESPACE" \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

echo "Waiting for Tempo to be ready..."
kubectl rollout status statefulset/tempo -n "$NAMESPACE" --timeout=120s || true

echo "Tempo installed successfully."
