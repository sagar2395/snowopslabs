#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="argocd"

echo "Uninstalling ArgoCD..."

# Uninstall ArgoCD
helm uninstall argocd -n $NAMESPACE >/dev/null 2>&1 || true

# Delete namespace if it exists
kubectl delete namespace $NAMESPACE --ignore-not-found --timeout=60s >/dev/null 2>&1 || true

echo "ArgoCD uninstalled successfully"
