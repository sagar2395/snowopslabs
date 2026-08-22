#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="kyverno"
SCRIPT_DIR="$(dirname "$0")"

echo "Installing Kyverno..."

# Create namespace if it doesn't exist
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
echo "Adding Kyverno Helm repository..."
helm repo add kyverno https://kyverno.github.io/kyverno/ >/dev/null 2>&1 || true
helm repo update

# Install or upgrade Kyverno
echo "Installing Kyverno chart..."
helm upgrade --install kyverno kyverno/kyverno \
  --namespace $NAMESPACE \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml"

# Wait for Kyverno to be ready
echo "Waiting for Kyverno to be ready..."
kubectl rollout status deployment/kyverno-admission-controller -n $NAMESPACE --timeout=120s || true

echo ""
echo "Kyverno installed successfully"
echo "Namespace: $NAMESPACE"
echo "Status: kubectl get pods -n $NAMESPACE"
echo ""
echo "Verify policies: kubectl get clusterpolicies"
echo "View policy reports: kubectl get policyreports -A"
