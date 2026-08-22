#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="vault"
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"

echo "=== HashiCorp Vault Status ==="
echo ""

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "Vault is not installed (namespace $NAMESPACE not found)"
  exit 0
fi

# Pod readiness + seal status (dev mode is always unsealed)
echo "Server:"
if kubectl get pod vault-0 -n "$NAMESPACE" >/dev/null 2>&1; then
  ready=$(kubectl get pod vault-0 -n "$NAMESPACE" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
  echo "  vault-0 Ready: ${ready:-Unknown}"
  sealed=$(kubectl exec -n "$NAMESPACE" vault-0 -- sh -c \
    "VAULT_ADDR=http://127.0.0.1:8200 vault status -format=json 2>/dev/null" 2>/dev/null |
    grep -o '"sealed"[: ]*[a-z]*' | head -1 || echo "")
  [ -n "$sealed" ] && echo "  ${sealed}"
else
  echo "  vault-0 pod not found"
fi
echo ""

echo "Pods:"
kubectl get pods -n "$NAMESPACE" -o wide 2>/dev/null || echo "  No pods found"
echo ""

echo "UI ingress:"
host=$(kubectl get ingress vault-ui -n "$NAMESPACE" -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || echo "")
if [ -n "$host" ]; then
  echo "  http://${host}"
else
  echo "  not found (expected vault.${DOMAIN_SUFFIX})"
fi
echo ""

echo "Demo secret presence (secret/go-api):"
if kubectl exec -n "$NAMESPACE" vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${VAULT_DEV_ROOT_TOKEN:-root}' vault kv get -field=api-key secret/go-api" >/dev/null 2>&1; then
  echo "  present (key: api-key)"
else
  echo "  not found — re-run install.sh to seed, or check your dev root token"
fi
