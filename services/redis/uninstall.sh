#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="services"

echo "Uninstalling Redis..."

# Uninstall Redis
helm uninstall redis -n $NAMESPACE >/dev/null 2>&1 || true

# Delete PVCs created by Redis
echo "Deleting Redis PVCs..."
kubectl delete pvc -n $NAMESPACE -l app.kubernetes.io/name=redis >/dev/null 2>&1 || true

echo "Redis uninstalled successfully"
