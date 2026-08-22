#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="kyverno"

echo "Uninstalling Kyverno..."

# Uninstall Kyverno
helm uninstall kyverno -n $NAMESPACE >/dev/null 2>&1 || true

# Clean up CRDs (Kyverno CRDs are not removed by helm uninstall)
echo "Removing Kyverno CRDs..."
kubectl delete crd -l app.kubernetes.io/part-of=kyverno >/dev/null 2>&1 || true

# Delete namespace
kubectl delete namespace $NAMESPACE --ignore-not-found --timeout=60s >/dev/null 2>&1 || true

echo "Kyverno uninstalled successfully"
