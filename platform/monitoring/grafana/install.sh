#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VALUES_FILE="$(mktemp "${TMPDIR:-/tmp}/grafana-values.XXXXXX")"
trap 'rm -f "$VALUES_FILE"' EXIT

# DOMAIN_SUFFIX is provided by the executor environment (from .env + runtime.env).
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"

echo "Installing Grafana..."
sed "s/\\.monitoring\\.svc/.$NAMESPACE.svc/g" "$SCRIPT_DIR/values.yaml" >"$VALUES_FILE"

# Create namespace if it doesn't exist
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
echo "Adding Grafana Helm repository..."
helm repo add grafana https://grafana.github.io/helm-charts --force-update
helm repo update

# Get admin password from env or use default
GRAFANA_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD:-admin}"

# Install or upgrade Grafana with dynamic ingress host
echo "Installing Grafana chart..."
helm upgrade --install grafana grafana/grafana \
  --namespace "$NAMESPACE" \
  --create-namespace \
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
