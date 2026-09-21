#!/usr/bin/env bash
set -eu
. "$(dirname "$0")/../../_lib/workload.sh"

# Grades the LESSON: the rotation reached the pod without a redeploy.
#
# "No redeploy" is a claim about a specific pod — the one that was already
# running when the drill started — still running now, having picked up a value it
# was not started with. So it is graded against a checkpoint the activation
# recorded, not against a literal generation of 1, which is only ever an accident
# of a fresh install and turns red the first time anything legitimately touches
# the Deployment.
#
# It stays pending until a rotation has actually happened. Before that there is
# nothing to have propagated in place, and passing would mean "the pod you just
# installed has not restarted yet" — which was never in doubt.

NS="${WORKLOAD_NAMESPACE}"
ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_consumer.sh
. "${SCRIPT_DIR}/../scripts/_consumer.sh"

CHECKPOINT="$(checkpoint consumer-pod-uid)"
BASELINE="$(checkpoint baseline)"
BASELINE="${BASELINE:-${SECRETS_BASELINE_VALUE:-baseline-v1}}"

if [ -z "$CHECKPOINT" ]; then
  echo "NOT COMPLETE: no checkpoint was recorded, so 'the same pod' cannot be identified." >&2
  echo "Re-activate the scenario: labctl scenario up secrets-management --force" >&2
  exit 1
fi

CURRENT="$(kubectl exec -n vault vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv get -field=api-key secret/${WORKLOAD_NAME}" 2>/dev/null || true)"
if [ "$CURRENT" = "$BASELINE" ]; then
  echo "PENDING: nothing has been rotated yet, so no propagation has been asked of the pod." >&2
  echo "Rotate in Vault first, then re-verify." >&2
  exit 1
fi

POD="$(consumer_pod_name)"
if [ -z "$POD" ]; then
  echo "FAIL: no running secret-consumer pod in namespace ${NS}." >&2
  exit 1
fi

UID_NOW="$(kubectl -n "$NS" get pod "$POD" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
RESTARTS="$(kubectl -n "$NS" get pod "$POD" \
  -o jsonpath='{.status.containerStatuses[0].restartCount}' 2>/dev/null || echo "")"

if [ "$UID_NOW" != "$CHECKPOINT" ]; then
  echo "FAIL: this is not the pod the drill started with." >&2
  echo "  at activation: ${CHECKPOINT}" >&2
  echo "  now:           ${UID_NOW:-<none>}" >&2
  echo "A replacement pod reads the Secret fresh at startup, so it proves nothing about" >&2
  echo "in-place propagation — that IS the redeploy a file mount is supposed to avoid." >&2
  echo "Reset and rotate again without touching the Deployment:" >&2
  echo "  labctl scenario reset secrets-management" >&2
  exit 1
fi

if [ "${RESTARTS:-1}" != "0" ]; then
  echo "FAIL: the consumer container restarted ${RESTARTS} time(s)." >&2
  echo "The process that is reading the rotated value is not the one that was running" >&2
  echo "before it. Reset and rotate again: labctl scenario reset secrets-management" >&2
  exit 1
fi

echo "OK: pod ${POD} (${UID_NOW}) is the one that was running before the rotation, with 0 restarts."
echo "The new value reached a process that was never restarted — no redeploy needed."
