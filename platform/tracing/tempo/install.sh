#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../../_lib/helm.sh
. "$(cd "$SCRIPT_DIR/../.." && pwd)/_lib/helm.sh"
GRAFANA_CHARTS_REPO="${GRAFANA_CHARTS_REPO:-https://grafana-community.github.io/helm-charts}"
TEMPO_CHART_VERSION="${TEMPO_CHART_VERSION:-2.3.0}"
VALUES_FILE="$(mktemp "${TMPDIR:-/tmp}/tempo-values.XXXXXX")"
trap 'rm -f "$VALUES_FILE"' EXIT

echo "Installing Tempo..."

# The grafana/tempo chart is deprecated; Tempo is maintained in grafana-community.
helm repo add grafana-community "$GRAFANA_CHARTS_REPO" --force-update
helm repo update grafana-community

# Clean up stuck pending releases if any
if helm status tempo -n "$NAMESPACE" 2>/dev/null | grep -q "pending-"; then
  echo "Cleaning up stuck Tempo release..."
  helm delete tempo -n "$NAMESPACE" --wait 2>/dev/null || true
fi

# Render with the real monitoring namespace, the same way grafana's install does
# — values.yaml names in-cluster services that move with the namespace.
sed "s/\.monitoring\.svc/.${NAMESPACE}.svc/g" "$SCRIPT_DIR/values.yaml" >"$VALUES_FILE"

helm_upgrade_install tempo "$NAMESPACE" grafana-community/tempo \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$TEMPO_CHART_VERSION" \
  -f "$VALUES_FILE" \
  --wait --timeout 5m

echo "Waiting for Tempo to be ready..."
kubectl rollout status statefulset/tempo -n "$NAMESPACE" --timeout=120s || true

echo "Tempo installed successfully."
