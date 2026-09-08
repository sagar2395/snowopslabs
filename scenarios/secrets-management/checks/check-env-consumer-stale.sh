#!/usr/bin/env bash
set -eu

# Grades the CONTRAST, which is the lesson: after a rotation reaches the
# file-mounted consumer, the env-var consumer is still holding the old value.
# The order of the tests below matters — "not rotated yet" and "still
# propagating" are pending states, and only a genuine loss of the contrast
# (the env pod restarted and re-read the Secret) is a failure.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
BASELINE="${SECRETS_BASELINE_VALUE:-baseline-v1}"

pod_for() {
  kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get pod -l "app=$1" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

FILE_POD="$(pod_for secret-consumer)"
ENV_POD="$(pod_for env-consumer)"
if [ -z "$FILE_POD" ] || [ -z "$ENV_POD" ]; then
  echo "NOT COMPLETE: expected both secret-consumer and env-consumer pods in namespace ${WORKLOAD_NAMESPACE:-go-api}." >&2
  exit 1
fi

WANT="$(kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv get -field=api-key secret/${WORKLOAD_NAME:-go-api}" 2>/dev/null || true)"
if [ -z "$WANT" ]; then
  echo "NOT COMPLETE: could not read secret/${WORKLOAD_NAME:-go-api} from Vault." >&2
  exit 1
fi

if [ "$WANT" = "$BASELINE" ]; then
  echo "PENDING: secret/${WORKLOAD_NAME:-go-api} is still the baseline ('${BASELINE}') — rotate it first." >&2
  exit 1
fi

FILE_VALUE="$(kubectl -n ${WORKLOAD_NAMESPACE:-go-api} exec "$FILE_POD" -- cat /etc/api/api-key 2>/dev/null || true)"
ENV_VALUE="$(kubectl -n ${WORKLOAD_NAMESPACE:-go-api} exec "$ENV_POD" -- sh -c 'echo "$API_KEY"' 2>/dev/null || true)"

if [ "$FILE_VALUE" != "$WANT" ]; then
  echo "PENDING: the rotation has not reached the file-mounted pod yet" >&2
  echo "(it reads '${FILE_VALUE:-<empty>}', Vault has '${WANT}'). Allow ~90s." >&2
  exit 1
fi

if [ "$FILE_VALUE" = "$ENV_VALUE" ]; then
  echo "NOT COMPLETE: both pods read '${ENV_VALUE}', so the contrast is gone." >&2
  echo "The env-var pod restarted and re-read the Secret at startup — that restart is" >&2
  echo "exactly the redeploy a file mount avoids. Reset and rotate again:" >&2
  echo "  labctl scenario reset secrets-management" >&2
  exit 1
fi

echo "OK: file-mounted pod reads '${FILE_VALUE}', env-var pod is still on '${ENV_VALUE}'."
echo "That is the difference a rotation makes to a file and not to an environment."
