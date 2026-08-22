#!/usr/bin/env bash
set -euo pipefail

# Stage 2: rotate the secret in Vault, then give ESO a moment to propagate it to
# the Kubernetes Secret (refreshInterval is 10s). The rotation-propagated check
# asserts the new value landed.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
ROTATED="${SECRETS_ROTATED_VALUE:-scenario-secret-v2}"

echo "Rotating secret/go-api (api-key) in Vault to '${ROTATED}'..."
kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv put secret/go-api api-key='${ROTATED}'" >/dev/null

echo "Waiting for External Secrets to propagate the new value (refreshInterval ~10s)..."
sleep 15

echo "Rotation complete (value: ${ROTATED}). The synced Secret should now match."
