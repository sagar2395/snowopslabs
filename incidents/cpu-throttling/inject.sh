#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="cpu-throttling"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

deploy_get() {
  kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={$1}" 2>/dev/null || true
}

# Guard on the fault itself: the limit it set is still in the pod template.
FAULT_LIMIT="$(fault_state_get "$FAULT" fault-limit)"
LIMIT_NOW="$(deploy_get .spec.template.spec.containers[0].resources.limits.cpu)"
if [ -n "$FAULT_LIMIT" ] && [ "$LIMIT_NOW" = "$FAULT_LIMIT" ]; then
  echo "Fault already injected - nothing to do."
  exit 0
fi
REQUEST_NOW="$(deploy_get .spec.template.spec.containers[0].resources.requests.cpu)"

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator: the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Applying the cost review's resource changes to $NS/$DEPLOY..."

# The steady load the limit is sized against, and that keeps it biting.
SVC_PORT="$(kubectl -n "$NS" get svc "$DEPLOY" -o 'jsonpath={.spec.ports[0].port}' 2>/dev/null || true)"
TRAFFIC_PROFILE=steady \
  TRAFFIC_TARGET="http://$DEPLOY.$NS.svc.cluster.local:${SVC_PORT:-8080}/" \
  TRAFFIC_RPS="${FAULT_RPS:-60}" \
  TRAFFIC_DURATION="${FAULT_DURATION:-30m}" \
  bash "$PROJECT_ROOT/src/services/traffic/start.sh" >/dev/null 2>&1 ||
  echo "Note: traffic generator not started - run 'labctl traffic start --app $DEPLOY --rps 60' to make the limit bite."

# Measure what one pod really uses under that load, before any limit is
# lowered. kubectl top reports a window that lags the load, and the first new
# readings can still average in idle time. So poll until the reading has
# changed three times, keeping the largest: 25 tries, 3s apart.
SELECTOR="$(workload_selector "$NS" "$DEPLOY")"
pod_cpu_max() {
  max=0
  for cpu in $(kubectl top pod -n "$NS" -l "$SELECTOR" --no-headers 2>/dev/null | awk '{ print $2 }' || true); do
    m="$(cpu_millicores "$cpu")"
    [ "${m:-0}" -le "$max" ] || max="$m"
  done
  echo "$max"
}
DEMAND=0
last="$(pod_cpu_max)"
changes=0
tries=0
while [ "$changes" -lt 3 ] && [ "$tries" -lt 25 ]; do
  sleep "${FAULT_SAMPLE_SECONDS:-3}"
  tries=$((tries + 1))
  now="$(pod_cpu_max)"
  if [ "$now" != "$last" ]; then
    changes=$((changes + 1))
    last="$now"
  fi
  [ "$now" -le "$DEMAND" ] || DEMAND="$now"
done
# Without metrics-server, what go-api uses at 60 requests a second.
[ "$DEMAND" -gt 0 ] || DEMAND=40

# A third of the demand, and never below the 10m the kernel can enforce.
LIMIT_M=$((DEMAND / 3))
[ "$LIMIT_M" -ge 10 ] || LIMIT_M=10
FAULT_LIMIT="${LIMIT_M}m"
REQUEST="$FAULT_LIMIT"
if [ -n "$REQUEST_NOW" ] && [ "$(cpu_millicores "$REQUEST_NOW")" -lt "$LIMIT_M" ]; then
  REQUEST="$REQUEST_NOW"
fi

fault_state_save "$FAULT" \
  "original-request=${REQUEST_NOW:-none}" \
  "original-limit=${LIMIT_NOW:-none}" \
  "demand-millicores=$DEMAND" \
  "fault-limit=$FAULT_LIMIT"

kubectl -n "$NS" set resources "deploy/$DEPLOY" \
  --limits="cpu=$FAULT_LIMIT" --requests="cpu=$REQUEST" >/dev/null
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s >/dev/null 2>&1 || true
echo "Done."
