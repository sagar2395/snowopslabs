#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="liveness-probe-kill"
MARK="labfault-$FAULT"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

probe_get() {
  kubectl -n "$NS" get deploy "$DEPLOY" \
    -o "jsonpath={.spec.template.spec.containers[0].livenessProbe$1}" 2>/dev/null || true
}

ORIG_PATH="$(fault_state_get "$FAULT" original-path)"
ORIG_PROBE="$(fault_state_get "$FAULT" original-probe)"
PROBE_NOW="$(probe_get "")"
PATH_NOW="$(probe_get .httpGet.path)"

# The learner's probe stays only when it really passes on a running pod.
probe_passes() {
  [ -n "$PATH_NOW" ] || return 0
  port="$(container_port "$NS" "$DEPLOY" "$(probe_get .httpGet.port)")"
  pod="$(running_pod "$NS" "$(workload_selector "$NS" "$DEPLOY")")"
  [ -n "$port" ] && [ -n "$pod" ] && pod_get_ok "$NS" "$pod" "$port" "$PATH_NOW" "$(probe_get .httpGet.scheme)"
}

if [ -z "$PROBE_NOW" ] && [ -n "$ORIG_PROBE" ]; then
  echo "Restoring $DEPLOY's liveness probe..."
  kubectl -n "$NS" patch deploy "$DEPLOY" --type=json \
    -p "[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/livenessProbe\",\"value\":$ORIG_PROBE}]" >/dev/null
elif [ -z "$PROBE_NOW" ]; then
  echo "$DEPLOY has no liveness probe and no record of the original. Add one back by hand:"
  echo "  kubectl -n $NS edit deploy $DEPLOY"
elif [ -n "$ORIG_PATH" ] && [ "$PATH_NOW" != "$ORIG_PATH" ] && ! probe_passes; then
  echo "Restoring $DEPLOY's liveness probe..."
  kubectl -n "$NS" patch deploy "$DEPLOY" --type=json \
    -p "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/livenessProbe/httpGet/path\",\"value\":\"$ORIG_PATH\"}]" >/dev/null
elif [ -z "$ORIG_PATH" ] && [ "$PATH_NOW" = "/healthz" ] && ! probe_passes; then
  echo "No record of the original probe path. Point it at the app's health endpoint by hand:"
  echo "  kubectl -n $NS patch deploy $DEPLOY --type=json -p '[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/livenessProbe/httpGet/path\",\"value\":\"/health\"}]'"
else
  echo "Liveness probe already fixed."
fi

fault_state_clear "$FAULT"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found 2>/dev/null || true

# inject.sh started the load that shows the errors, so resolve stops it. This
# also stops a run the user started themselves.
echo "Stopping the traffic generator this fault started..."
bash "$PROJECT_ROOT/src/services/traffic/stop.sh" >/dev/null 2>&1 || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
