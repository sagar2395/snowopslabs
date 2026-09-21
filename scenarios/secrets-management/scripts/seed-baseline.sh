#!/usr/bin/env bash
set -eu
. "$(dirname "$0")/../../_lib/workload.sh"

# Stage 1: put a known baseline value in Vault so the drill has a defined starting
# point, and wait for it to reach the consumer pod. The Vault->Secret wiring itself
# belongs to the secrets/external-secrets platform component and is not touched
# here — this scenario only seeds a value and mounts the result.
#
# Config (env, never committed):
#   VAULT_DEV_ROOT_TOKEN   dev root token (default: root)
#   SECRETS_BASELINE_VALUE baseline written to secret/<app> (default below)

ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
BASELINE="${SECRETS_BASELINE_VALUE:-baseline-v1}"
NS="${WORKLOAD_NAMESPACE}"
ES="${WORKLOAD_NAME}-secret"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_consumer.sh
. "${SCRIPT_DIR}/_consumer.sh"

# Record what the consumer looked like before the drill: which pod is serving,
# and at what Deployment generation. "No redeploy was needed" is a claim about
# THIS pod still running afterwards, and it can only be graded against a value
# captured now — a literal generation of 1 is an accident of a fresh install, not
# evidence of anything.
record_checkpoint() {
  # Wait for the pod that will still be running at the end of the drill; a
  # checkpoint taken against a pod that has not settled would fail the very
  # check it exists to make gradeable.
  kubectl -n "$NS" rollout status deploy/secret-consumer --timeout=120s >/dev/null 2>&1 || true
  uid="$(consumer_pod_uid)"
  gen="$(kubectl -n "$NS" get deploy secret-consumer \
    -o jsonpath='{.metadata.generation}' 2>/dev/null || true)"
  [ -n "$uid" ] || return 0
  kubectl -n "$NS" create configmap secret-rotation-checkpoint \
    --from-literal=consumer-pod-uid="$uid" \
    --from-literal=consumer-generation="${gen:-0}" \
    --from-literal=baseline="$BASELINE" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  echo "Checkpoint recorded: consumer pod ${uid} at generation ${gen}."
}

echo "Seeding baseline secret/${WORKLOAD_NAME} api-key=${BASELINE} in Vault..."
kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv put secret/${WORKLOAD_NAME} api-key='${BASELINE}'" >/dev/null

# Break ESO out of its backoff before waiting on it.
#
# Vault runs in dev mode and keeps its KV in memory, so any restart of vault-0
# empties it. ESO then fails to read secret/<workload> and backs off
# exponentially — after a few hours it is retrying on the order of hours, not the
# 15s refreshInterval. Re-seeding the value alone does not help: the reconcile
# that would notice it is not due for a long time. The force-sync annotation is
# ESO's own idiom for "reconcile this one now", and it resets the backoff.
kubectl -n "$NS" annotate externalsecret "$ES" \
  force-sync="$(date -u +%s)" --overwrite >/dev/null

echo "Waiting for External Secrets to sync the baseline into ${NS}/${WORKLOAD_NAME}-secrets..."
deadline=$(($(date +%s) + 120))
while [ "$(date +%s)" -lt "$deadline" ]; do
  got="$(kubectl -n "$NS" get secret "${WORKLOAD_NAME}-secrets" \
    -o go-template='{{index .data "api-key" | base64decode}}' 2>/dev/null || true)"
  if [ "$got" = "$BASELINE" ]; then
    echo "Baseline synced. The consumer pod now reads '${BASELINE}' from /etc/api/api-key."
    record_checkpoint
    exit 0
  fi
  sleep 5
done

# Report what ESO actually says. Sending the learner to 'describe externalsecret'
# is worse than useless here: the condition it prints is whatever failed LAST,
# which after a re-seed is a stale "Secret does not exist" about a value that is
# now sitting in Vault.
REASON="$(kubectl -n "$NS" get externalsecret "$ES" \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}: {.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null || true)"
echo "Baseline did not sync within 120s." >&2
echo "  ExternalSecret ${NS}/${ES} reports: ${REASON:-<no Ready condition>}" >&2
echo "  Vault holds: $(kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv get -field=api-key secret/${WORKLOAD_NAME}" 2>/dev/null || echo '<unreadable>')" >&2
echo "  If Vault holds the baseline but ESO does not, the SecretStore is misconfigured:" >&2
echo "    kubectl -n ${NS} get secretstore vault-backend -o yaml" >&2
exit 1
