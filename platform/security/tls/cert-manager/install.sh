#!/usr/bin/env bash
set -euo pipefail

CERT_MANAGER_CHART_VERSION="${CERT_MANAGER_CHART_VERSION:-v1.21.1}"

NAMESPACE="cert-manager"
SCRIPT_DIR="$(dirname "$0")"

echo "Installing cert-manager..."

# Create namespace if it doesn't exist
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
echo "Adding cert-manager Helm repository..."
helm repo add jetstack https://charts.jetstack.io >/dev/null 2>&1 || true
helm repo update

# Install or upgrade cert-manager (CRDs included via Helm)
echo "Installing cert-manager chart..."
helm upgrade --install cert-manager jetstack/cert-manager \
  --version "$CERT_MANAGER_CHART_VERSION" \
  --namespace $NAMESPACE \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml"

# Wait for cert-manager to be ready
echo "Waiting for cert-manager to be ready..."
kubectl rollout status deployment/cert-manager -n $NAMESPACE --timeout=120s || true
kubectl rollout status deployment/cert-manager-webhook -n $NAMESPACE --timeout=120s || true

# Apply self-signed ClusterIssuer for local dev
echo "Creating self-signed ClusterIssuer..."
kubectl apply -f "$SCRIPT_DIR/cluster-issuer.yaml"

echo ""
echo "cert-manager installed successfully"
echo "Namespace: $NAMESPACE"
echo "Status: kubectl get pods -n $NAMESPACE"
echo ""
echo "ClusterIssuers: kubectl get clusterissuers"
echo "Certificates: kubectl get certificates -A"
