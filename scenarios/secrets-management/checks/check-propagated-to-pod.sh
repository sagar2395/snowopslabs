#!/usr/bin/env bash
set -eu

# Grades the OUTCOME: the file inside the running consumer pod must equal what
# Vault currently holds. This is the end of the chain — Vault -> ESO -> Secret ->
# kubelet -> the file a process reads — and it is what "rotation works" means.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
NS="${WORKLOAD_NAMESPACE:-go-api}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_consumer.sh
. "${SCRIPT_DIR}/../scripts/_consumer.sh"

BASELINE="$(checkpoint baseline)"
BASELINE="${BASELINE:-${SECRETS_BASELINE_VALUE:-baseline-v1}}"

WANT="$(kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv get -field=api-key secret/${WORKLOAD_NAME:-go-api}" 2>/dev/null || true)"
if [ -z "$WANT" ]; then
  echo "NOT COMPLETE: could not read secret/${WORKLOAD_NAME:-go-api} from Vault." >&2
  exit 1
fi

# Before a rotation the file and Vault agree because nothing has moved, and
# passing on that would grade the stage rather than the drill.
if [ "$WANT" = "$BASELINE" ]; then
  echo "PENDING: secret/${WORKLOAD_NAME:-go-api} is still the baseline ('${BASELINE}')." >&2
  echo "The file already matches it, which says nothing about propagation — rotate first." >&2
  exit 1
fi

POD="$(consumer_pod_name)"
if [ -z "$POD" ]; then
  echo "NOT COMPLETE: no running secret-consumer pod in namespace ${NS}." >&2
  exit 1
fi

GOT="$(kubectl -n "$NS" exec "$POD" -- cat /etc/api/api-key 2>/dev/null || true)"

if [ "$GOT" = "$WANT" ]; then
  echo "OK: the running pod reads the current Vault value from /etc/api/api-key."
  exit 0
fi

echo "PENDING: pod has '${GOT:-<empty>}', Vault has '${WANT}'." >&2
echo "ESO refreshes every 15s and the kubelet updates the mounted file on its own" >&2
echo "cycle — propagation to the file takes up to ~90s. Give it another minute." >&2
exit 1
