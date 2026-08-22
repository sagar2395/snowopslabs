#!/usr/bin/env bash
set -euo pipefail

# External Secrets Operator (ESO), wired to Vault as its backend.
# Demonstrates the full sync chain:
#   Vault KV (secret/go-api) -> ExternalSecret -> k8s Secret -> go-api env var.
# Portable + idempotent.
#
# ESO depends on Vault. This script PREFLIGHTS for Vault rather than installing
# it (install secrets/vault first).
#
# Config (env, with defaults — scripts never source .env themselves):
#   ESO_CHART_VERSION     pinned external-secrets chart version (versions.env)
#   VAULT_DEV_ROOT_TOKEN  Vault token ESO authenticates with (default: root)
#   SECRETS_NAMESPACE     namespace to sync the secret into (default: go-api)
#   VAULT_NAMESPACE       namespace where Vault runs (default: vault)

NAMESPACE="external-secrets"
CHART_VERSION="${ESO_CHART_VERSION:-0.10.5}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
TARGET_NS="${SECRETS_NAMESPACE:-go-api}"
VAULT_NS="${VAULT_NAMESPACE:-vault}"

# --- Preflight: Vault must already be installed --------------------------------
if ! kubectl get svc vault -n "$VAULT_NS" >/dev/null 2>&1; then
  echo "ERROR: Vault not found (service 'vault' in namespace '$VAULT_NS')." >&2
  echo "Install it first:  labctl platform up secrets/vault" >&2
  exit 1
fi

echo "Installing External Secrets Operator ${CHART_VERSION} (namespace=${NAMESPACE})..."

helm repo add external-secrets https://charts.external-secrets.io --force-update
helm repo update external-secrets

helm upgrade --install external-secrets external-secrets/external-secrets \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$CHART_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --set installCRDs=true \
  --wait --timeout 5m

echo "Waiting for the ESO controller + webhook to be ready..."
kubectl rollout status deployment/external-secrets -n "$NAMESPACE" --timeout=180s
kubectl rollout status deployment/external-secrets-webhook -n "$NAMESPACE" --timeout=180s

# Ensure the target namespace exists.
kubectl create namespace "$TARGET_NS" --dry-run=client -o yaml | kubectl apply -f -

# Token ESO uses to talk to Vault. Sourced from env — never committed to git.
echo "Creating Vault token secret in '$TARGET_NS' (from env, not committed)..."
kubectl create secret generic vault-token \
  --namespace "$TARGET_NS" \
  --from-literal=token="$ROOT_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

# SecretStore (Vault backend) + ExternalSecret (sync secret/go-api -> k8s Secret).
echo "Wiring SecretStore + ExternalSecret in '$TARGET_NS' ..."
cat <<EOF | kubectl apply -f -
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: vault-backend
  namespace: ${TARGET_NS}
spec:
  provider:
    vault:
      server: "http://vault.${VAULT_NS}.svc:8200"
      path: "secret"
      version: "v2"
      auth:
        tokenSecretRef:
          name: vault-token
          key: token
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: go-api-secret
  namespace: ${TARGET_NS}
spec:
  refreshInterval: 15s
  secretStoreRef:
    name: vault-backend
    kind: SecretStore
  target:
    name: go-api-secrets
    creationPolicy: Owner
  data:
    - secretKey: api-key
      remoteRef:
        key: go-api
        property: api-key
EOF

echo "Waiting for the ExternalSecret to sync..."
kubectl wait externalsecret/go-api-secret -n "$TARGET_NS" \
  --for=condition=Ready --timeout=120s || true

echo ""
echo "External Secrets Operator installed and wired."
echo "    Synced secret: '${TARGET_NS}/go-api-secrets' (key: api-key)"
echo "    Refresh interval: 15s — rotate the Vault value and watch it propagate."
echo "    Wire into go-api with envFrom.secretRef.name=go-api-secrets (see runbook)."
