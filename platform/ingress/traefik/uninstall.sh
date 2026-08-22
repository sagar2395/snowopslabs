#!/usr/bin/env bash
set -euo pipefail

NAMESPACE=traefik

echo "Uninstalling Traefik..."

# Uninstall Helm release
if helm status traefik -n $NAMESPACE >/dev/null 2>&1; then
  helm uninstall traefik -n $NAMESPACE
  echo "Helm release removed."
else
  echo "Helm release 'traefik' not found in namespace '$NAMESPACE'."
fi

# Delete namespace
if kubectl get namespace $NAMESPACE >/dev/null 2>&1; then
  kubectl delete namespace $NAMESPACE --timeout=60s || true
  echo "Namespace '$NAMESPACE' deleted."
fi

echo ""
echo "Traefik uninstalled."
