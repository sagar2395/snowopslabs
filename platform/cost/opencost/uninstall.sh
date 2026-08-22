#!/usr/bin/env bash
set -euo pipefail

# Remove OpenCost. Idempotent.

NAMESPACE="opencost"

echo "Uninstalling OpenCost..."

if helm status opencost -n "$NAMESPACE" >/dev/null 2>&1; then
  helm uninstall opencost -n "$NAMESPACE"
fi

if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  kubectl delete namespace "$NAMESPACE" --timeout=60s || true
fi

echo "OpenCost uninstalled."
