#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="services"

echo "Redis Status:"
echo "============="
echo ""

if ! kubectl get namespace $NAMESPACE &>/dev/null; then
  echo "Namespace '$NAMESPACE' not found"
  exit 1
fi

echo "Pods:"
kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=redis

echo ""
echo "Services:"
kubectl get svc -n $NAMESPACE -l app.kubernetes.io/name=redis

echo ""
echo "PVCs:"
kubectl get pvc -n $NAMESPACE -l app.kubernetes.io/name=redis

echo ""
echo "Connection info:"
echo "  Host: redis-master.services.svc.cluster.local"
echo "  Port: 6379"
echo "  URL:  redis://redis-master.services.svc.cluster.local:6379"
