#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="ingress-nginx"

echo "Uninstalling Nginx Ingress Controller..."

# Uninstall Helm release
if helm status ingress-nginx -n $NAMESPACE >/dev/null 2>&1; then
  helm uninstall ingress-nginx -n $NAMESPACE
  echo "Helm release removed."
else
  echo "Helm release 'ingress-nginx' not found in namespace '$NAMESPACE'."
fi

# Delete namespace
if kubectl get namespace $NAMESPACE >/dev/null 2>&1; then
  kubectl delete namespace $NAMESPACE --timeout=60s || true
  echo "Namespace '$NAMESPACE' deleted."
fi

echo ""
echo "Nginx Ingress Controller uninstalled."
