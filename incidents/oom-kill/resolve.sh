#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="oom-kill"
MARK="labfault-$FAULT"
# shellcheck source=incidents/_lib/state.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/state.sh"

ORIG_LIMIT="$(fault_state_get "$FAULT" original-limit)"
ORIG_REQUEST="$(fault_state_get "$FAULT" original-request)"

# inject.sh writes the literal "none" when the workload had no memory setting of
# its own. Restoring that as a number would leave the lab in a state it was never
# in - and a value invented for this repo's small Go services may itself OOM a
# JVM. "none" means remove the setting, which `set resources` spells as 0.
had_none() {
  case "${1:-}" in
    "" | none | 0) return 0 ;;
    *) return 1 ;;
  esac
}

# `set resources --limits=memory=0` does NOT remove the setting: kubectl writes a
# literal "0" into the spec. The workload is then left in a state it was never in,
# and the next injection records that "0" as the value to restore - so the wrong
# state cements itself and every later resolve reproduces it. Removing the key is
# a JSON patch, and it has to tolerate the key already being absent.
drop_memory() {
  kubectl -n "$NS" patch "deploy/$DEPLOY" --type=json \
    -p "[{\"op\":\"remove\",\"path\":\"/spec/template/spec/containers/0/resources/$1/memory\"}]" \
    >/dev/null 2>&1 || true
}

if [ -z "$ORIG_LIMIT" ] && [ -z "$ORIG_REQUEST" ]; then
  # inject.sh records "none" for a setting the workload did not have, so an
  # empty record means this fault was never injected here. Removing the memory
  # settings then would strip a healthy workload of its own.
  echo "No record of the original memory settings - leaving them as they are."
  echo "  If the limit is still the fault's, raise it yourself:"
  echo "    kubectl -n $NS set resources deploy/$DEPLOY --limits=memory=<value> --requests=memory=<value>"
elif had_none "$ORIG_LIMIT" && had_none "$ORIG_REQUEST"; then
  echo "Restoring memory settings (the workload had none of its own; removing them)..."
  drop_memory limits
  drop_memory requests
else
  echo "Restoring memory settings (limit=${ORIG_LIMIT:-none}, request=${ORIG_REQUEST:-none})..."
  if had_none "$ORIG_LIMIT"; then drop_memory limits; else
    kubectl -n "$NS" set resources "deploy/$DEPLOY" --limits="memory=$ORIG_LIMIT" >/dev/null
  fi
  if had_none "$ORIG_REQUEST"; then drop_memory requests; else
    kubectl -n "$NS" set resources "deploy/$DEPLOY" --requests="memory=$ORIG_REQUEST" >/dev/null
  fi
fi

fault_state_clear "$FAULT"
# Drop the annotations a lab may still carry on the Deployment.
kubectl -n "$NS" annotate deploy "$DEPLOY" \
  "$MARK-" "$MARK-original-limit-" "$MARK-original-request-" \
  --overwrite >/dev/null 2>&1 || true

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found 2>/dev/null || true

# inject.sh started the load that makes the limit bite, so resolve owns stopping
# it. This also stops a run the user started themselves - say so rather than
# leaving them wondering where their traffic went.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
echo "Stopping the traffic generator this fault started..."
bash "$PROJECT_ROOT/src/services/traffic/stop.sh" >/dev/null 2>&1 || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
