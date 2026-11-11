#!/usr/bin/env bash
set -euo pipefail

# Remove the ESO wiring (ClusterSecretStore + Vault token) and the operator
# itself. Idempotent. Does NOT touch Vault. The ExternalSecrets that read
# through the store belong to the scenarios that applied them.

NAMESPACE="external-secrets"

echo "Uninstalling External Secrets Operator..."

# 1. Remove the store while the operator and its webhook are still running.
kubectl delete clustersecretstore vault-backend --ignore-not-found || true

# 2. Remove the operator (Helm release) and its namespace, which holds the
#    vault-token secret.
if helm status external-secrets -n "$NAMESPACE" >/dev/null 2>&1; then
  helm uninstall external-secrets -n "$NAMESPACE"
fi
if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  kubectl delete secret vault-token -n "$NAMESPACE" --ignore-not-found || true
  kubectl delete namespace "$NAMESPACE" --timeout=60s || true
fi

echo ""
echo "External Secrets Operator uninstalled."
