#!/usr/bin/env bash
set -eu

# Grades the LESSON: the rotation must have reached the pod without a redeploy.
# If the consumer ever restarted, the file could have been re-read at startup and
# the drill would prove nothing about in-place propagation.

POD="$(kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get pod -l app=secret-consumer \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [ -z "$POD" ]; then
  echo "FAIL: no secret-consumer pod found in namespace ${WORKLOAD_NAMESPACE:-go-api}." >&2
  exit 1
fi

RESTARTS="$(kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get pod "$POD" \
  -o jsonpath='{.status.containerStatuses[0].restartCount}' 2>/dev/null || echo "")"
GENERATION="$(kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get deploy secret-consumer \
  -o jsonpath='{.status.observedGeneration}' 2>/dev/null || echo "")"

if [ "${RESTARTS:-1}" != "0" ]; then
  echo "FAIL: the consumer container restarted ${RESTARTS} time(s)." >&2
  echo "Rotation is supposed to reach the pod in place. Re-run the drill without" >&2
  echo "restarting or redeploying secret-consumer." >&2
  exit 1
fi

if [ "${GENERATION:-0}" != "1" ]; then
  echo "FAIL: secret-consumer was redeployed (generation ${GENERATION})." >&2
  echo "The point of the drill is that no redeploy is needed. Run" >&2
  echo "'labctl scenario reset secrets-management' and rotate without touching the Deployment." >&2
  exit 1
fi

echo "OK: no restart and no redeploy — the rotation reached the pod in place."
