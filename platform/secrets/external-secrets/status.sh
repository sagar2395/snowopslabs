#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="external-secrets"
TARGET_NS="${SECRETS_NAMESPACE:-go-api}"

echo "=== External Secrets Operator Status ==="
echo ""

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "ESO is not installed (namespace $NAMESPACE not found)"
  exit 0
fi

# Controller health
echo "Operator:"
for d in external-secrets external-secrets-webhook external-secrets-cert-controller; do
  if kubectl get deployment "$d" -n "$NAMESPACE" >/dev/null 2>&1; then
    ready=$(kubectl get deployment "$d" -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    desired=$(kubectl get deployment "$d" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)
    echo "  $d: ${ready:-0}/${desired:-0} ready"
  fi
done
echo ""

# SecretStore + ExternalSecret readiness in the target namespace
echo "Sync wiring in '$TARGET_NS':"
store=$(kubectl get secretstore vault-backend -n "$TARGET_NS" \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
echo "  SecretStore vault-backend Ready: ${store:-NotFound}"
es=$(kubectl get externalsecret go-api-secret -n "$TARGET_NS" \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
echo "  ExternalSecret go-api-secret Ready: ${es:-NotFound}"
echo ""

# The synced k8s Secret produced by the ExternalSecret
echo "Synced Secret '${TARGET_NS}/go-api-secrets':"
if kubectl get secret go-api-secrets -n "$TARGET_NS" >/dev/null 2>&1; then
  keys=$(kubectl get secret go-api-secrets -n "$TARGET_NS" -o jsonpath='{.data}' 2>/dev/null |
    grep -o '"[^"]*":' | tr -d '":' | tr '\n' ' ' || echo "")
  echo "  present (keys: ${keys:-?})"
  echo "  reveal: kubectl -n $TARGET_NS get secret go-api-secrets -o go-template='{{index .data \"api-key\" | base64decode}}{{\"\\n\"}}'"
else
  echo "  not found — check the ExternalSecret status above"
fi
