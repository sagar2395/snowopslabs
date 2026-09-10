#!/usr/bin/env bash
set -euo pipefail

# inflate.sh — put ${WORKLOAD_NAME:-go-api} into the deliberately over-provisioned 'before' state
# (40x CPU / 32x memory) so the waste is visible in OpenCost before you right-size.
#
# We mutate the running Deployment in place with `kubectl set resources` — the
# exact inverse of the fix the learner performs (`kubectl set resources ...
# --requests=cpu=50m,memory=32Mi`). This is deliberately symmetric and robust:
# it works regardless of how ${WORKLOAD_NAME:-go-api} was deployed (helm release, `kubectl apply`,
# or the kind-e2e bootstrap) and regardless of which field-manager owns the
# resource fields. An earlier `helm upgrade` approach failed with server-side
# apply conflicts ("conflicts with kubectl") whenever ${WORKLOAD_NAME:-go-api} was not owned by
# helm — `kubectl set resources` sidesteps that entirely.

NAMESPACE="${NAMESPACE:-${WORKLOAD_NAMESPACE:-go-api}}"
APP="${APP:-${WORKLOAD_NAME:-go-api}}"

# The inflated 'before' values. Requests drive scheduling and node billing;
# limits are matched to requests here so the pod is a single fat, wasteful slot.
# Prefixed, because CPU_REQUEST / MEMORY_REQUEST are the names an app's own
# app.env uses for its contractual sizing. Sharing them would let a sourced
# app.env silently make this script a no-op — the scenario would activate
# already right-sized and every check would be green before the learner starts.
INFLATED_CPU_REQUEST="${INFLATED_CPU_REQUEST:-2000m}"
INFLATED_MEM_REQUEST="${INFLATED_MEM_REQUEST:-1Gi}"
INFLATED_CPU_LIMIT="${INFLATED_CPU_LIMIT:-2000m}"
INFLATED_MEM_LIMIT="${INFLATED_MEM_LIMIT:-1Gi}"

if ! kubectl get deployment "$APP" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "ERROR: deployment ${NAMESPACE}/${APP} not found." >&2
  echo "Deploy it first: labctl app deploy ${APP}" >&2
  exit 1
fi

# Record the sizing the workload arrived with, so `scenario down` can put it
# back. Without this the scenario permanently mutates a workload every other
# scenario shares, and teardown silently leaves it over-provisioned.
MARK="snowops.net/cost-right-sizing-original"
ORIG_CPU_REQ="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.cpu}' 2>/dev/null || true)"
ORIG_MEM_REQ="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || true)"
ORIG_CPU_LIM="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.cpu}' 2>/dev/null || true)"
ORIG_MEM_LIM="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true)"
kubectl -n "$NAMESPACE" annotate deploy "$APP" \
  "$MARK-cpu-request=${ORIG_CPU_REQ:-none}" "$MARK-mem-request=${ORIG_MEM_REQ:-none}" \
  "$MARK-cpu-limit=${ORIG_CPU_LIM:-none}" "$MARK-mem-limit=${ORIG_MEM_LIM:-none}" \
  --overwrite >/dev/null

echo "Over-provisioning ${NAMESPACE}/${APP}: requests cpu=${INFLATED_CPU_REQUEST}, memory=${INFLATED_MEM_REQUEST} (limits ${INFLATED_CPU_LIMIT}/${INFLATED_MEM_LIMIT})..."
kubectl -n "$NAMESPACE" set resources deployment "$APP" \
  --requests="cpu=${INFLATED_CPU_REQUEST},memory=${INFLATED_MEM_REQUEST}" \
  --limits="cpu=${INFLATED_CPU_LIMIT},memory=${INFLATED_MEM_LIMIT}"

echo "Waiting for the over-provisioned pod to roll out..."
kubectl -n "$NAMESPACE" rollout status deployment "$APP" --timeout=5m

echo "Done. ${APP} now requests ${INFLATED_CPU_REQUEST} CPU / ${INFLATED_MEM_REQUEST} memory (the inflated baseline)."
echo "Observe the cost in OpenCost, then right-size with 'kubectl -n ${NAMESPACE} set resources'."
