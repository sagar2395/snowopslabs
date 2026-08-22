#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"

echo "=== Loki Status ==="
kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=loki 2>/dev/null || echo "Loki not found"

echo ""
echo "=== Promtail Status ==="
kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=promtail 2>/dev/null || echo "Promtail not found"

echo ""
echo "=== Loki Services ==="
kubectl get svc -n "$NAMESPACE" -l app.kubernetes.io/name=loki 2>/dev/null || echo "No Loki services"
