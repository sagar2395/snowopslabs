#!/usr/bin/env bash
set -eu

# Reverses seed-baseline.sh on 'scenario down': puts back the demo value that the
# secrets/vault platform component seeds, so tearing the scenario down leaves the
# platform exactly as it found it. Idempotent; never fails the teardown.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
DEMO_SECRET="${VAULT_DEMO_SECRET:-s3cr3t-from-vault-v1}"

if ! kubectl get pod vault-0 -n vault >/dev/null 2>&1; then
  echo "Vault is not running; nothing to restore."
  exit 0
fi

echo "Restoring the platform's demo value at secret/go-api..."
kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv put secret/go-api api-key='${DEMO_SECRET}'" >/dev/null 2>&1 ||
  echo "Could not restore the demo value (Vault may be sealed or restarted); harmless."

echo "Vault restored to the platform baseline."
