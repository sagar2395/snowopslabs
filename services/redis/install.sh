#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="services"
SCRIPT_DIR="$(dirname "$0")"

echo "Installing Redis..."

# Create namespace if it doesn't exist
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
echo "Adding Bitnami Helm repository..."
helm repo add bitnami https://charts.bitnami.com/bitnami >/dev/null 2>&1 || true
helm repo update

# Install or upgrade Redis
echo "Installing Redis chart..."
helm upgrade --install redis bitnami/redis \
  --namespace $NAMESPACE \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml"

# Wait for Redis master to be ready
echo "Waiting for Redis to be ready..."
kubectl rollout status statefulset/redis-master -n $NAMESPACE --timeout=120s || true

echo "Redis installed successfully"
echo ""
echo "Connection info:"
echo "  Host: redis-master.services.svc.cluster.local"
echo "  Port: 6379"
echo "  URL:  redis://redis-master.services.svc.cluster.local:6379"
echo "Namespace: $NAMESPACE"
echo "Status: kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=redis"
