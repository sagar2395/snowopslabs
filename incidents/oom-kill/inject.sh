#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-echo-server}"
DEPLOY="${TARGET_WORKLOAD:-echo-server}"
MARK="labfault-oom-kill"

# Idle echo-server sits near 3Mi; under load it needs about 10Mi. A limit
# between the two reproduces the failure that actually happens in production —
# healthy at rest, OOMKilled once real traffic arrives — rather than a pod that
# never starts. The traffic below is what makes the limit bite.
FAULT_LIMIT="${FAULT_LIMIT:-8Mi}"
FAULT_REQUEST="${FAULT_REQUEST:-4Mi}"

# Trust the cluster, not just the marker. An annotation left behind by a partial
# resolve, or a limit someone restored by hand, makes the marker alone claim the
# fault is live when it is not — and the detection check then grades a healthy
# lab as already fixed.
CURRENT_LIMIT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true)"
if [ "$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK}" 2>/dev/null)" = "injected" ] &&
  [ "$CURRENT_LIMIT" = "$FAULT_LIMIT" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl apply -n "$MON_NS" -f "$SCRIPT_DIR/alerts/rule.yaml" 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

ORIG_LIMIT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true)"
ORIG_REQUEST="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || true)"

echo "Injecting: cutting $DEPLOY's memory limit to $FAULT_LIMIT..."
kubectl -n "$NS" annotate deploy "$DEPLOY" \
  "$MARK=injected" \
  "$MARK-original-limit=${ORIG_LIMIT:-none}" \
  "$MARK-original-request=${ORIG_REQUEST:-none}" \
  --overwrite
kubectl -n "$NS" set resources "deploy/$DEPLOY" \
  --limits="memory=$FAULT_LIMIT" --requests="memory=$FAULT_REQUEST"

echo "Driving allocation with the k6 traffic generator..."
TRAFFIC_PROFILE=write \
  TRAFFIC_TARGET="http://$DEPLOY.$NS.svc.cluster.local:8080/" \
  TRAFFIC_RPS="${FAULT_RPS:-60}" \
  TRAFFIC_DURATION="${FAULT_DURATION:-30m}" \
  sh "$PROJECT_ROOT/src/services/traffic/start.sh"

# An injection that returns before the symptom exists is how this fault used to
# grade as already-fixed: the detection check ran in the window between the
# limit change and the first kill, passed, and closed the incident.
echo "Waiting for the first OOMKill..."
i=0
while [ "$i" -lt 60 ]; do
  if kubectl -n "$NS" get pods -o 'jsonpath={.items[*].status.containerStatuses[*].lastState.terminated.reason}' 2>/dev/null | grep -q OOMKilled; then
    echo "Injected. $DEPLOY is being OOMKilled under load."
    exit 0
  fi
  i=$((i + 1))
  sleep 5
done

echo "Injected the limit and started traffic, but no OOMKill appeared in 5 minutes." >&2
echo "The fault is not live. Check that the traffic generator is running:" >&2
echo "  labctl traffic status" >&2
exit 1
