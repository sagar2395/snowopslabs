#!/usr/bin/env bash
set -eu
. "$(dirname "$0")/../../_lib/workload.sh"

# Grades the CONTRAST, which is the lesson: after a rotation reaches the
# file-mounted consumer, the env-var consumer is still holding the old value.
# The order of the tests below matters — "not rotated yet" and "still
# propagating" are pending states, and only a genuine loss of the contrast
# (the env pod restarted and re-read the Secret) is a failure.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_consumer.sh
. "${SCRIPT_DIR}/../scripts/_consumer.sh"

BASELINE="$(checkpoint baseline)"
BASELINE="${BASELINE:-${SECRETS_BASELINE_VALUE:-baseline-v1}}"

FILE_POD="$(consumer_pod_name secret-consumer)"
ENV_POD="$(consumer_pod_name env-consumer)"
if [ -z "$FILE_POD" ] || [ -z "$ENV_POD" ]; then
  echo "NOT COMPLETE: expected both secret-consumer and env-consumer pods running in ${NS}." >&2
  exit 1
fi

WANT="$(kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv get -field=api-key secret/${WORKLOAD_NAME}" 2>/dev/null || true)"
if [ -z "$WANT" ]; then
  echo "NOT COMPLETE: could not read secret/${WORKLOAD_NAME} from Vault." >&2
  exit 1
fi

if [ "$WANT" = "$BASELINE" ]; then
  echo "PENDING: secret/${WORKLOAD_NAME} is still the baseline ('${BASELINE}') — rotate it first." >&2
  exit 1
fi

FILE_VALUE="$(kubectl -n "$NS" exec "$FILE_POD" -- cat /etc/api/api-key 2>/dev/null || true)"
ENV_VALUE="$(kubectl -n "$NS" exec "$ENV_POD" -- sh -c 'echo "$API_KEY"' 2>/dev/null || true)"

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
