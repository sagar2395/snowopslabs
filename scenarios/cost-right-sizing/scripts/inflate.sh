#!/usr/bin/env bash
set -euo pipefail

# inflate.sh — put go-api into the deliberately over-provisioned 'before' state
# (40x CPU / 32x memory) so the waste is visible in OpenCost before you right-size.
#
# We mutate the running Deployment in place with `kubectl set resources` — the
# exact inverse of the fix the learner performs (`kubectl set resources ...
# --requests=cpu=50m,memory=32Mi`). This is deliberately symmetric and robust:
# it works regardless of how go-api was deployed (helm release, `kubectl apply`,
# or the kind-e2e bootstrap) and regardless of which field-manager owns the
# resource fields. An earlier `helm upgrade` approach failed with server-side
# apply conflicts ("conflicts with kubectl") whenever go-api was not owned by
# helm — `kubectl set resources` sidesteps that entirely.

NAMESPACE="${NAMESPACE:-go-api}"
APP="${APP:-go-api}"

# The inflated 'before' values. Requests drive scheduling and node billing;
# limits are matched to requests here so the pod is a single fat, wasteful slot.
CPU_REQUEST="${CPU_REQUEST:-2000m}"
MEM_REQUEST="${MEM_REQUEST:-1Gi}"
CPU_LIMIT="${CPU_LIMIT:-2000m}"
MEM_LIMIT="${MEM_LIMIT:-1Gi}"

if ! kubectl get deployment "$APP" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "ERROR: deployment ${NAMESPACE}/${APP} not found." >&2
  echo "Deploy it first: labctl app deploy ${APP}" >&2
  exit 1
fi

echo "Over-provisioning ${NAMESPACE}/${APP}: requests cpu=${CPU_REQUEST}, memory=${MEM_REQUEST} (limits ${CPU_LIMIT}/${MEM_LIMIT})..."
kubectl -n "$NAMESPACE" set resources deployment "$APP" \
  --requests="cpu=${CPU_REQUEST},memory=${MEM_REQUEST}" \
  --limits="cpu=${CPU_LIMIT},memory=${MEM_LIMIT}"

echo "Waiting for the over-provisioned pod to roll out..."
kubectl -n "$NAMESPACE" rollout status deployment "$APP" --timeout=5m

echo "Done. ${APP} now requests ${CPU_REQUEST} CPU / ${MEM_REQUEST} memory (the inflated baseline)."
echo "Observe the cost in OpenCost, then right-size with 'kubectl -n ${NAMESPACE} set resources'."
