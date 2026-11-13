#!/usr/bin/env bash
set -euo pipefail

# External Secrets Operator (ESO), wired to Vault as its backend.
# Demonstrates the full sync chain:
#   Vault KV (secret/<workload>) -> ExternalSecret -> k8s Secret -> workload env var.
# Portable + idempotent.
#
# ESO depends on Vault. This script PREFLIGHTS for Vault rather than installing
# it (install secrets/vault first).
#
# Nothing here names an app: the platform installs the operator and one
# ClusterSecretStore that every namespace can read through. A scenario applies
# the ExternalSecret for the app it is bound to, so one install serves any app.
#
# Config (env, with defaults - scripts never source .env themselves):
#   ESO_CHART_VERSION     pinned external-secrets chart version (config/versions.env)
#   VAULT_DEV_ROOT_TOKEN  Vault token ESO authenticates with (default: root)
#   VAULT_NAMESPACE       namespace where Vault runs (default: vault)

NAMESPACE="external-secrets"
CHART_VERSION="${ESO_CHART_VERSION:-0.10.5}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
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

# Token ESO uses to talk to Vault. Sourced from env - never committed to git.
# It sits next to the operator; the ClusterSecretStore names its namespace.
echo "Creating Vault token secret in '$NAMESPACE' (from env, not committed)..."
kubectl create secret generic vault-token \
  --namespace "$NAMESPACE" \
  --from-literal=token="$ROOT_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

# One ClusterSecretStore (Vault backend). An ExternalSecret in any namespace
# reads through it with secretStoreRef kind: ClusterSecretStore.
echo "Wiring ClusterSecretStore vault-backend ..."
cat <<EOF | kubectl apply -f -
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: vault-backend
spec:
  provider:
    vault:
      server: "http://vault.${VAULT_NS}.svc:8200"
      path: "secret"
      version: "v2"
      auth:
        tokenSecretRef:
          name: vault-token
          namespace: ${NAMESPACE}
          key: token
EOF

echo "Waiting for the ClusterSecretStore to reach Vault..."
kubectl wait clustersecretstore/vault-backend \
  --for=condition=Ready --timeout=120s || true

echo ""
echo "External Secrets Operator installed and wired."
echo "    ClusterSecretStore: vault-backend (Vault KV v2, path secret/)"
echo "    Sync a Vault key into a namespace with an ExternalSecret whose"
echo "    secretStoreRef is kind: ClusterSecretStore, name: vault-backend."
