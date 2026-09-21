#!/usr/bin/env bash
set -eu
. "$(dirname "$0")/../../_lib/workload.sh"

# Grades LEARNER work: the value in Vault must no longer be the baseline stage 1
# seeded. Any rotation the learner performs satisfies this; no specific value is
# demanded, so the drill is not a guess-the-magic-string exercise.

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
BASELINE="${SECRETS_BASELINE_VALUE:-baseline-v1}"

CURRENT="$(kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv get -field=api-key secret/${WORKLOAD_NAME}" 2>/dev/null || true)"

if [ -z "$CURRENT" ]; then
  echo "NOT COMPLETE: could not read secret/${WORKLOAD_NAME} from Vault." >&2
  exit 1
fi

if [ "$CURRENT" = "$BASELINE" ]; then
  echo "PENDING: secret/${WORKLOAD_NAME} is still the baseline ('${BASELINE}') — you have not rotated it yet." >&2
  exit 1
fi

echo "OK: secret/${WORKLOAD_NAME} was rotated away from the baseline."
