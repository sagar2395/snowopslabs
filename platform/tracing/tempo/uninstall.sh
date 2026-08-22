#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"

echo "Uninstalling Tempo..."
helm uninstall tempo --namespace "$NAMESPACE" 2>/dev/null || true

# Clean up PVCs
kubectl delete pvc -l app.kubernetes.io/name=tempo -n "$NAMESPACE" --ignore-not-found

echo "Tempo uninstalled."
