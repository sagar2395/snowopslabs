#!/usr/bin/env bash
set -eu
. "$(dirname "$0")/../../_lib/workload.sh"

# Reverses seed-baseline.sh on 'scenario down': puts back the demo value that the
# secrets/vault platform component seeds, so tearing the scenario down leaves the
# platform exactly as it found it. Idempotent; never fails the teardown.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
DEMO_SECRET="${VAULT_DEMO_SECRET:-s3cr3t-from-vault-v1}"
NS="${WORKLOAD_NAMESPACE}"

# The checkpoint describes a pod this teardown is about to delete, so it goes
# with it. Leaving it would let the next activation grade against a pod UID that
# no longer exists.
kubectl -n "$NS" delete configmap secret-rotation-checkpoint --ignore-not-found

if ! kubectl get pod vault-0 -n vault >/dev/null 2>&1; then
  echo "Vault is not running; nothing to restore."
  exit 0
fi

echo "Restoring the platform's demo value at secret/${WORKLOAD_NAME}..."
kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv put secret/${WORKLOAD_NAME} api-key='${DEMO_SECRET}'" >/dev/null 2>&1 ||
  echo "Could not restore the demo value (Vault may be sealed or restarted); harmless."

# Put ESO back in step immediately rather than leaving the synced Secret holding
# the drill's rotated value until the next refresh happens to land.
kubectl -n "$NS" annotate externalsecret "${WORKLOAD_NAME}-secret" \
  force-sync="$(date -u +%s)" --overwrite >/dev/null 2>&1 || true

echo "Vault restored to the platform baseline."
