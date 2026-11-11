#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="external-secrets"

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

# The store every ExternalSecret reads Vault through
store=$(kubectl get clustersecretstore vault-backend \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
echo "ClusterSecretStore vault-backend Ready: ${store:-NotFound}"
echo ""

# The ExternalSecrets scenarios applied, and the Secret each one writes
echo "ExternalSecrets:"
list=$(kubectl get externalsecrets --all-namespaces \
  -o jsonpath='{range .items[*]}  {.metadata.namespace}/{.metadata.name} -> Secret {.spec.target.name}, Ready: {.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' 2>/dev/null || echo "")
if [ -n "$list" ]; then
  printf '%s\n' "$list"
  echo "  reveal: kubectl -n <namespace> get secret <secret> -o go-template='{{index .data \"api-key\" | base64decode}}{{\"\\n\"}}'"
else
  echo "  none - a scenario such as secrets-management applies one for its app"
fi
