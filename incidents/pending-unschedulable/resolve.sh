#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="pending-unschedulable"
MARK="labfault-$FAULT"
LIB="$(cd "$(dirname "$0")/../_lib" && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

deploy_get() {
  kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={$1}" 2>/dev/null || true
}

ORIG_REPLICAS="$(fault_state_get "$FAULT" original-replicas)"
ORIG_REQUEST="$(fault_state_get "$FAULT" original-request)"
ORIG_LIMIT="$(fault_state_get "$FAULT" original-limit)"
FAULT_REQUEST="$(fault_state_get "$FAULT" fault-request)"
LAUNCH_REPLICAS="$(fault_state_get "$FAULT" launch-replicas)"

REQUEST_NOW="$(deploy_get .spec.template.spec.containers[0].resources.requests.cpu)"
LIMIT_NOW="$(deploy_get .spec.template.spec.containers[0].resources.limits.cpu)"
REPLICAS_NOW="$(deploy_get .spec.replicas)"

# Remove a setting the workload never had; `set resources` would write "0".
drop_cpu() {
  kubectl -n "$NS" patch "deploy/$DEPLOY" --type=json \
    -p "[{\"op\":\"remove\",\"path\":\"/spec/template/spec/containers/0/resources/$1/cpu\"}]" \
    >/dev/null 2>&1 || true
}

if [ -z "$FAULT_REQUEST" ]; then
  # Without the record nothing says which request is the fault's.
  echo "No record of the original CPU settings, leaving them as they are."
  echo "  If replicas are still Pending, lower the request yourself:"
  echo "    kubectl -n $NS set resources deploy/$DEPLOY --requests=cpu=<value> --limits=cpu=<value>"
else
  # A changed request is the learner's fix only if the pods now schedule; a
  # value that still fits no node is put back like the fault's own.
  SELECTOR="$(workload_selector "$NS" "$DEPLOY")"
  UNSCHEDULED=""
  if [ -n "$SELECTOR" ]; then
    UNSCHEDULED="$(kubectl -n "$NS" get pods -l "$SELECTOR" \
      --field-selector=status.phase=Pending,spec.nodeName= -o name 2>/dev/null || true)"
  fi
  if [ "$REQUEST_NOW" = "$FAULT_REQUEST" ] || [ -n "$UNSCHEDULED" ]; then
    echo "Restoring $DEPLOY's CPU settings..."
    if [ "$ORIG_LIMIT" = "none" ]; then drop_cpu limits; fi
    if [ "$ORIG_REQUEST" = "none" ]; then
      drop_cpu requests
      if [ "$ORIG_LIMIT" != "none" ]; then
        kubectl -n "$NS" set resources "deploy/$DEPLOY" --limits="cpu=$ORIG_LIMIT" >/dev/null
      fi
    elif [ "$ORIG_LIMIT" = "none" ]; then
      kubectl -n "$NS" set resources "deploy/$DEPLOY" --requests="cpu=$ORIG_REQUEST" >/dev/null
    else
      kubectl -n "$NS" set resources "deploy/$DEPLOY" \
        --requests="cpu=$ORIG_REQUEST" --limits="cpu=$ORIG_LIMIT" >/dev/null
    fi
    if [ "$REPLICAS_NOW" = "$LAUNCH_REPLICAS" ] && [ -n "$ORIG_REPLICAS" ]; then
      kubectl -n "$NS" scale "deploy/$DEPLOY" --replicas="$ORIG_REPLICAS" >/dev/null
    fi
  elif [ "$LIMIT_NOW" = "$FAULT_REQUEST" ] && [ -n "$ORIG_LIMIT" ]; then
    # The request was fixed and the limit the fault raised with it was not.
    echo "Restoring $DEPLOY's CPU limit..."
    if [ "$ORIG_LIMIT" = "none" ]; then
      drop_cpu limits
    elif ! kubectl -n "$NS" set resources "deploy/$DEPLOY" --limits="cpu=$ORIG_LIMIT" >/dev/null 2>&1; then
      echo "  Left the limit at $LIMIT_NOW: your request is above the original limit ($ORIG_LIMIT)."
    fi
  else
    echo "CPU settings already fixed."
  fi
fi

fault_state_clear "$FAULT"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
