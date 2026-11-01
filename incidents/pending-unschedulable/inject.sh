#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="pending-unschedulable"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

# The launch scales to 4, or by two for a workload already that large.
LAUNCH_REPLICAS=4

deploy_get() {
  kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={$1}" 2>/dev/null || true
}

# Guard on the fault itself: the request it set is still in the pod template.
FAULT_REQUEST="$(fault_state_get "$FAULT" fault-request)"
if [ -n "$FAULT_REQUEST" ] &&
  [ "$(deploy_get .spec.template.spec.containers[0].resources.requests.cpu)" = "$FAULT_REQUEST" ]; then
  echo "Fault already injected - nothing to do."
  exit 0
fi

# With no cluster answering, the change is staged unsized, as other faults do.
CLUSTER_UP="$(kubectl get nodes -o name 2>/dev/null || true)"

REPLICAS="$(deploy_get .spec.replicas)"
if [ -z "$REPLICAS" ]; then
  if [ -n "$CLUSTER_UP" ]; then
    echo "ERROR: deployment $NS/$DEPLOY not found. Deploy it first: labctl app deploy $DEPLOY" >&2
    exit 1
  fi
  REPLICAS=1
fi

# An autoscaler owns the replica count and would undo the scale-up.
if kubectl -n "$NS" get hpa -o 'jsonpath={range .items[*]}{.spec.scaleTargetRef.name}{"\n"}{end}' 2>/dev/null |
  grep -qx "$DEPLOY"; then
  echo "ERROR: a HorizontalPodAutoscaler manages $NS/$DEPLOY's replicas, so a fixed scale-up would not hold." >&2
  echo "  Remove it for the drill, or inject into another app:" >&2
  echo "    kubectl -n $NS get hpa" >&2
  exit 1
fi

# One whole core more than the largest node can allocate. The free CPU on a node
# grows whenever a pod leaves it; its allocatable capacity does not, so no node
# can ever hold the pod, on a laptop or a large lab.
MAX_ALLOCATABLE=0
for cpu in $(kubectl get nodes -o 'jsonpath={range .items[*]}{.status.allocatable.cpu}{" "}{end}' 2>/dev/null || true); do
  m="$(cpu_millicores "$cpu")"
  [ "${m:-0}" -le "$MAX_ALLOCATABLE" ] || MAX_ALLOCATABLE="$m"
done
if [ "$MAX_ALLOCATABLE" -le 0 ]; then
  if [ -n "$CLUSTER_UP" ]; then
    echo "ERROR: cannot read the nodes' allocatable CPU. Check it with: kubectl get nodes -o wide" >&2
    exit 1
  fi
  echo "No cluster reachable - staging the change without node sizing."
  MAX_ALLOCATABLE=63000
fi
FAULT_REQUEST="$((MAX_ALLOCATABLE / 1000 + 1))"

# A request no node can hold is already this outage. Recording it as the
# original would make resolve put the broken value back.
REQUEST_NOW="$(deploy_get .spec.template.spec.containers[0].resources.requests.cpu)"
if [ "$(cpu_millicores "$REQUEST_NOW")" -gt "$MAX_ALLOCATABLE" ] 2>/dev/null; then
  echo "ERROR: $NS/$DEPLOY already requests $REQUEST_NOW CPU, more than any node can allocate." >&2
  echo "  Give it a request that fits first: kubectl -n $NS set resources deploy/$DEPLOY --requests=cpu=<value>" >&2
  exit 1
fi

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator: the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

ORIG_REQUEST="$(deploy_get .spec.template.spec.containers[0].resources.requests.cpu)"
ORIG_LIMIT="$(deploy_get .spec.template.spec.containers[0].resources.limits.cpu)"
CONTAINER="$(deploy_get .spec.template.spec.containers[0].name)"
[ "$REPLICAS" -lt "$LAUNCH_REPLICAS" ] || LAUNCH_REPLICAS="$((REPLICAS + 2))"

fault_state_save "$FAULT" \
  "original-replicas=$REPLICAS" \
  "original-request=${ORIG_REQUEST:-none}" \
  "original-limit=${ORIG_LIMIT:-none}" \
  "fault-request=$FAULT_REQUEST" \
  "launch-replicas=$LAUNCH_REPLICAS"

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Rolling out a change to $NS/$DEPLOY..."

# The launch is one change: more replicas and a bigger request together. The
# Deployment scales the old ReplicaSet up with the new count, and the rolling
# update keeps all but maxUnavailable of them serving while the new pods wait
# for a node. A limit below the request is invalid, so a limit moves with it.
RESOURCES="{\"requests\":{\"cpu\":\"$FAULT_REQUEST\"}}"
if [ -n "$ORIG_LIMIT" ]; then
  RESOURCES="{\"requests\":{\"cpu\":\"$FAULT_REQUEST\"},\"limits\":{\"cpu\":\"$FAULT_REQUEST\"}}"
fi
kubectl -n "$NS" patch deploy "$DEPLOY" --type=strategic \
  -p "{\"spec\":{\"replicas\":$LAUNCH_REPLICAS,\"template\":{\"spec\":{\"containers\":[{\"name\":\"$CONTAINER\",\"resources\":$RESOURCES}]}}}}" >/dev/null

# Return once the symptom exists, so the first status check sees it.
# FAULT_WAIT_SECONDS=0 stages the fault without waiting, for the hermetic tests.
WAIT_SECONDS="${FAULT_WAIT_SECONDS:-60}"
SELECTOR="$(workload_selector "$NS" "$DEPLOY")"
waited=0
while [ "$waited" -lt "$WAIT_SECONDS" ]; do
  if [ -n "$(kubectl -n "$NS" get pods -l "$SELECTOR" --field-selector=status.phase=Pending,spec.nodeName= -o name 2>/dev/null || true)" ]; then
    break
  fi
  sleep 2
  waited=$((waited + 2))
done
echo "Done."
