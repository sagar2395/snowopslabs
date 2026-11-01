#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="cpu-throttling"
MARK="labfault-$FAULT"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

ORIG_REQUEST="$(fault_state_get "$FAULT" original-request)"
ORIG_LIMIT="$(fault_state_get "$FAULT" original-limit)"
DEMAND="$(fault_state_get "$FAULT" demand-millicores)"
LIMIT_NOW="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.cpu}' 2>/dev/null || true)"

# Remove a setting the workload never had; `set resources` would write "0".
drop_cpu() {
  kubectl -n "$NS" patch "deploy/$DEPLOY" --type=json \
    -p "[{\"op\":\"remove\",\"path\":\"/spec/template/spec/containers/0/resources/$1/cpu\"}]" \
    >/dev/null 2>&1 || true
}

# The same bar the check applies: twice the demand, capped at the original.
still_throttled() {
  [ -n "$LIMIT_NOW" ] || return 1
  need=$((DEMAND * 2))
  orig="$(cpu_millicores "$ORIG_LIMIT")"
  if [ -n "$orig" ] && [ "$orig" -lt "$need" ]; then need="$orig"; fi
  [ "$(cpu_millicores "$LIMIT_NOW")" -lt "$need" ]
}

if [ -z "$DEMAND" ]; then
  echo "No record of the original CPU settings - leaving them as they are."
  echo "  If the workload is still throttled, raise its limit yourself:"
  echo "    kubectl -n $NS set resources deploy/$DEPLOY --limits=cpu=<value> --requests=cpu=<value>"
elif still_throttled; then
  echo "Restoring $DEPLOY's CPU settings..."
  if [ "$ORIG_LIMIT" = "none" ]; then drop_cpu limits; fi
  if [ "$ORIG_REQUEST" = "none" ]; then drop_cpu requests; fi
  if [ "$ORIG_LIMIT" != "none" ] && [ "$ORIG_REQUEST" != "none" ]; then
    kubectl -n "$NS" set resources "deploy/$DEPLOY" \
      --limits="cpu=$ORIG_LIMIT" --requests="cpu=$ORIG_REQUEST" >/dev/null
  elif [ "$ORIG_LIMIT" != "none" ]; then
    kubectl -n "$NS" set resources "deploy/$DEPLOY" --limits="cpu=$ORIG_LIMIT" >/dev/null
  elif [ "$ORIG_REQUEST" != "none" ]; then
    kubectl -n "$NS" set resources "deploy/$DEPLOY" --requests="cpu=$ORIG_REQUEST" >/dev/null
  fi
else
  echo "CPU settings already fixed."
fi

fault_state_clear "$FAULT"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found 2>/dev/null || true

# inject.sh started the load the limit is sized against, so resolve stops it.
# This also stops a run the user started themselves.
echo "Stopping the traffic generator this fault started..."
bash "$PROJECT_ROOT/src/services/traffic/stop.sh" >/dev/null 2>&1 || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
