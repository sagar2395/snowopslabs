#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="ingress-nginx"
SCRIPT_DIR="$(dirname "$0")"

echo "Installing Nginx Ingress Controller..."

# Add Helm repo and update
echo "Adding ingress-nginx Helm repository..."
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx >/dev/null 2>&1 || true
helm repo update

# Install or upgrade Nginx Ingress Controller
echo "Installing ingress-nginx chart..."
helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace $NAMESPACE \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml"

# Wait for controller to be ready
echo "Waiting for Nginx Ingress Controller to be ready..."
kubectl rollout status deployment/ingress-nginx-controller -n $NAMESPACE --timeout=120s || true

echo ""
echo "Nginx Ingress Controller installed successfully"
echo "Namespace: $NAMESPACE"
echo "IngressClass: nginx"
echo "Status: kubectl get pods -n $NAMESPACE"
echo ""
echo "Verify IngressClass:"
echo "  kubectl get ingressclass"
