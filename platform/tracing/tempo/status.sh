#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"

echo "=== Tempo Status ==="
kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=tempo 2>/dev/null || echo "Tempo not found"

echo ""
echo "=== Tempo Services ==="
kubectl get svc -n "$NAMESPACE" -l app.kubernetes.io/name=tempo 2>/dev/null || echo "No Tempo services"
