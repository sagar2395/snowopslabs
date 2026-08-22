#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"

echo "Uninstalling Promtail..."
helm uninstall promtail --namespace "$NAMESPACE" 2>/dev/null || true

echo "Uninstalling Loki..."
helm uninstall loki --namespace "$NAMESPACE" 2>/dev/null || true

# Clean up PVCs
kubectl delete pvc -l app.kubernetes.io/name=loki -n "$NAMESPACE" --ignore-not-found

echo "Loki + Promtail uninstalled."
