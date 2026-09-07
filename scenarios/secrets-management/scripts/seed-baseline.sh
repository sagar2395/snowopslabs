#!/usr/bin/env bash
set -eu

# Stage 1: put a known baseline value in Vault so the drill has a defined starting
# point, and wait for it to reach the consumer pod. The Vault->Secret wiring itself
# belongs to the secrets/external-secrets platform component and is not touched
# here — this scenario only seeds a value and mounts the result.
#
# Config (env, never committed):
#   VAULT_DEV_ROOT_TOKEN   dev root token (default: root)
#   SECRETS_BASELINE_VALUE baseline written to secret/go-api (default below)

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
BASELINE="${SECRETS_BASELINE_VALUE:-baseline-v1}"

echo "Seeding baseline secret/go-api api-key=${BASELINE} in Vault..."
kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv put secret/go-api api-key='${BASELINE}'" >/dev/null

echo "Waiting for External Secrets to sync the baseline into go-api/go-api-secrets..."
deadline=$(($(date +%s) + 120))
while [ "$(date +%s)" -lt "$deadline" ]; do
  got="$(kubectl -n go-api get secret go-api-secrets \
    -o go-template='{{index .data "api-key" | base64decode}}' 2>/dev/null || true)"
  if [ "$got" = "$BASELINE" ]; then
    echo "Baseline synced. The consumer pod now reads '${BASELINE}' from /etc/api/api-key."
    exit 0
  fi
  sleep 5
done

echo "Baseline did not sync within 120s. Check: kubectl -n go-api describe externalsecret go-api-secret" >&2
exit 1
