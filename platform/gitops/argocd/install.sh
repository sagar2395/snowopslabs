#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="argocd"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"

echo "Installing ArgoCD..."

# Create namespace if it doesn't exist
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
echo "Adding ArgoCD Helm repository..."
helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
helm repo update

# Install or upgrade ArgoCD
echo "Installing ArgoCD chart..."
helm upgrade --install argocd argo/argo-cd \
  --namespace $NAMESPACE \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml" \
  --set server.service.type=ClusterIP \
  --set "server.ingress.hostname=argocd.${DOMAIN_SUFFIX}"

# Wait for ArgoCD server to be ready
echo "Waiting for ArgoCD server to be ready..."
kubectl rollout status deployment/argocd-server -n $NAMESPACE --timeout=120s || true

echo "ArgoCD installed successfully"
echo ""
echo "Access ArgoCD at: http://argocd.${DOMAIN_SUFFIX:-k3d.local}"
echo "Retrieve initial admin password: kubectl -n $NAMESPACE get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d"
echo "Namespace: $NAMESPACE"
echo "Status: kubectl get pods -n $NAMESPACE"
