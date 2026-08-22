#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="sealed-secrets"

echo "Uninstalling Sealed Secrets..."

# Uninstall Sealed Secrets
helm uninstall sealed-secrets -n $NAMESPACE >/dev/null 2>&1 || true

# Remove CRDs
echo "Removing Sealed Secrets CRDs..."
kubectl delete crd sealedsecrets.bitnami.com >/dev/null 2>&1 || true

# Delete namespace
kubectl delete namespace $NAMESPACE --ignore-not-found --timeout=60s >/dev/null 2>&1 || true

echo "Sealed Secrets uninstalled successfully"
