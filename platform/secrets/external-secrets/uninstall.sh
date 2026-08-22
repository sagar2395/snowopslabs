#!/usr/bin/env bash
set -euo pipefail

# Remove the ESO wiring (ExternalSecret/SecretStore/synced secret/token) and the
# operator itself. Idempotent. Does NOT touch Vault.

NAMESPACE="external-secrets"
TARGET_NS="${SECRETS_NAMESPACE:-go-api}"

echo "Uninstalling External Secrets Operator..."

# 1. Remove the sync wiring from the target namespace (leave the namespace — it
#    is usually the app's own namespace, owned by something else).
if kubectl get namespace "$TARGET_NS" >/dev/null 2>&1; then
  kubectl delete externalsecret go-api-secret -n "$TARGET_NS" --ignore-not-found || true
  kubectl delete secretstore vault-backend -n "$TARGET_NS" --ignore-not-found || true
  # The ExternalSecret owns go-api-secrets (creationPolicy: Owner); delete in case.
  kubectl delete secret go-api-secrets -n "$TARGET_NS" --ignore-not-found || true
  kubectl delete secret vault-token -n "$TARGET_NS" --ignore-not-found || true
fi

# 2. Remove the operator (Helm release) and its namespace.
if helm status external-secrets -n "$NAMESPACE" >/dev/null 2>&1; then
  helm uninstall external-secrets -n "$NAMESPACE"
fi
if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  kubectl delete namespace "$NAMESPACE" --timeout=60s || true
fi

echo ""
echo "External Secrets Operator uninstalled."
