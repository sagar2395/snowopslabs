#!/usr/bin/env bash
set -eu

# Grades the OUTCOME: the file inside the running consumer pod must equal what
# Vault currently holds. This is the end of the chain — Vault -> ESO -> Secret ->
# kubelet -> the file a process reads — and it is what "rotation works" means.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"

WANT="$(kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv get -field=api-key secret/${WORKLOAD_NAME:-go-api}" 2>/dev/null || true)"
if [ -z "$WANT" ]; then
  echo "NOT COMPLETE: could not read secret/${WORKLOAD_NAME:-go-api} from Vault." >&2
  exit 1
fi

POD="$(kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get pod -l app=secret-consumer \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [ -z "$POD" ]; then
  echo "NOT COMPLETE: no secret-consumer pod found in namespace ${WORKLOAD_NAMESPACE:-go-api}." >&2
  exit 1
fi

GOT="$(kubectl -n ${WORKLOAD_NAMESPACE:-go-api} exec "$POD" -- cat /etc/api/api-key 2>/dev/null || true)"

if [ "$GOT" = "$WANT" ]; then
  echo "OK: the running pod reads the current Vault value from /etc/api/api-key."
  exit 0
fi

echo "PENDING: pod has '${GOT:-<empty>}', Vault has '${WANT}'." >&2
echo "ESO refreshes every 15s and the kubelet updates the mounted file on its own" >&2
echo "cycle — propagation to the file takes up to ~90s. Give it another minute." >&2
exit 1
