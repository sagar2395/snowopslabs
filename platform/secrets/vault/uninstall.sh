#!/usr/bin/env bash
set -euo pipefail

# Remove Vault and its UI ingress. Idempotent.
# (Dev-mode data is in-memory, so there is no persistent state to scrub.)

NAMESPACE="vault"

echo "Uninstalling Vault..."

if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  kubectl delete ingress vault-ui -n "$NAMESPACE" --ignore-not-found || true
  if helm status vault -n "$NAMESPACE" >/dev/null 2>&1; then
    helm uninstall vault -n "$NAMESPACE"
  fi
  kubectl delete namespace "$NAMESPACE" --timeout=60s || true
fi

echo ""
echo "Vault uninstalled."
