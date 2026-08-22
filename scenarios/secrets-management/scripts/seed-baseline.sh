#!/usr/bin/env bash
set -euo pipefail

# Stage 1: ensure ESO can authenticate to Vault and seed the baseline secret.
# The Vault token comes from the environment (never committed); default is the
# well-known dev value 'root'. Idempotent.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
BASELINE="${SECRETS_BASELINE_VALUE:-scenario-secret-v1}"

echo "Ensuring vault-token secret exists in namespace go-api..."
kubectl create secret generic vault-token \
  --namespace go-api \
  --from-literal=token="$ROOT_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Seeding baseline secret/go-api (api-key) in Vault..."
kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv put secret/go-api api-key='${BASELINE}'" >/dev/null

echo "Baseline secret seeded (value: ${BASELINE})."
