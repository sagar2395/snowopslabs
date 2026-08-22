#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="sealed-secrets"
SCRIPT_DIR="$(dirname "$0")"

echo "Installing Sealed Secrets..."

# Create namespace if it doesn't exist
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
echo "Adding Sealed Secrets Helm repository..."
helm repo add sealed-secrets https://bitnami-labs.github.io/sealed-secrets >/dev/null 2>&1 || true
helm repo update

# Install or upgrade Sealed Secrets controller
echo "Installing Sealed Secrets chart..."
helm upgrade --install sealed-secrets sealed-secrets/sealed-secrets \
  --namespace $NAMESPACE \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml"

# Wait for controller to be ready
echo "Waiting for Sealed Secrets controller to be ready..."
kubectl rollout status deployment/sealed-secrets -n $NAMESPACE --timeout=120s || true

echo ""
echo "Sealed Secrets installed successfully"
echo "Namespace: $NAMESPACE"
echo "Status: kubectl get pods -n $NAMESPACE"
echo ""
echo "Install kubeseal CLI: brew install kubeseal (or see https://github.com/bitnami-labs/sealed-secrets#installation)"
echo ""
echo "Create a sealed secret:"
echo "  kubectl create secret generic my-secret --dry-run=client --from-literal=key=value -o yaml | \\"
echo "    kubeseal --controller-name=sealed-secrets --controller-namespace=$NAMESPACE -o yaml > sealed-secret.yaml"
