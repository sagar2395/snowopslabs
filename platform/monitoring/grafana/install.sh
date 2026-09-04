#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VALUES_FILE="$(mktemp "${TMPDIR:-/tmp}/grafana-values.XXXXXX")"
trap 'rm -f "$VALUES_FILE"' EXIT

# DOMAIN_SUFFIX is provided by the executor environment (from .env + runtime.env).
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"
GRAFANA_CHARTS_REPO="${GRAFANA_CHARTS_REPO:-https://grafana-community.github.io/helm-charts}"
GRAFANA_CHART_VERSION="${GRAFANA_CHART_VERSION:-13.2.0}"

echo "Installing Grafana..."
sed "s/\\.monitoring\\.svc/.$NAMESPACE.svc/g" "$SCRIPT_DIR/values.yaml" >"$VALUES_FILE"

# Create namespace if it doesn't exist
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Package the curated dashboards as a ConfigMap so the grafana chart can mount
# them (via dashboardsConfigMaps in values.yaml). This must exist before the
# chart install so the volume mount resolves. Idempotent via apply.
echo "Packaging Grafana dashboards..."
kubectl create configmap grafana-dashboards \
  --namespace "$NAMESPACE" \
  --from-file="$SCRIPT_DIR/provisioning/dashboards/" \
  --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
# The grafana/grafana chart is deprecated; it is maintained in grafana-community.
echo "Adding Grafana Helm repository..."
helm repo add grafana-community "$GRAFANA_CHARTS_REPO" --force-update
helm repo update grafana-community

# Get admin password from env or use default
GRAFANA_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD:-admin}"

# Install or upgrade Grafana with dynamic ingress host
echo "Installing Grafana chart..."
helm upgrade --install grafana grafana-community/grafana \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$GRAFANA_CHART_VERSION" \
  -f "$VALUES_FILE" \
  --set adminPassword="$GRAFANA_ADMIN_PASSWORD" \
  --set "ingress.hosts[0]=grafana.${DOMAIN_SUFFIX}" \
  --wait --timeout 5m

# Wait for Grafana deployment to be ready
echo "Waiting for Grafana to be ready..."
kubectl rollout status deployment/grafana -n "$NAMESPACE" --timeout=120s || true

echo "Grafana installed successfully"
echo ""
echo "Access Grafana at: http://grafana.${DOMAIN_SUFFIX}"
echo "Default credentials: admin / $GRAFANA_ADMIN_PASSWORD"
echo "Namespace: $NAMESPACE"
