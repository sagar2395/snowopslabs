#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="liveness-probe-kill"
# The path a values update renamed the probe to. The app serves /health, so
# every probe answers 404.
FAULT_PATH="/healthz"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

probe_get() {
  kubectl -n "$NS" get deploy "$DEPLOY" \
    -o "jsonpath={.spec.template.spec.containers[0].livenessProbe$1}" 2>/dev/null || true
}

# Guard on the fault itself, not on its record.
PATH_NOW="$(probe_get .httpGet.path)"
if [ "$PATH_NOW" = "$FAULT_PATH" ]; then
  echo "Fault already injected - nothing to do."
  exit 0
fi

PROBE="$(probe_get "")"
if [ -n "$(kubectl get nodes -o name 2>/dev/null || true)" ] && [ -z "$PATH_NOW" ]; then
  echo "ERROR: $NS/$DEPLOY has no HTTP liveness probe to change." >&2
  echo "  This fault needs one. Check with: kubectl -n $NS get deploy $DEPLOY -o yaml | grep -A 6 livenessProbe" >&2
  exit 1
fi

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator: the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

# The whole probe is kept, so resolve can put it back even if it was deleted.
fault_state_save "$FAULT" "original-path=${PATH_NOW:-/health}" "original-probe=${PROBE:-}"

# Pods running before the change: their probe events are not this fault's.
SELECTOR="$(workload_selector "$NS" "$DEPLOY")"
BEFORE=" $(kubectl -n "$NS" get pods -l "$SELECTOR" -o 'jsonpath={.items[*].metadata.name}' 2>/dev/null || true) "

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Rolling out a values update to $NS/$DEPLOY..."
kubectl -n "$NS" patch deploy "$DEPLOY" --type=json \
  -p "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/livenessProbe/httpGet/path\",\"value\":\"$FAULT_PATH\"}]" >/dev/null

# Requests make the restarts visible as bursts of errors on the dashboards.
SVC_PORT="$(kubectl -n "$NS" get svc "$DEPLOY" -o 'jsonpath={.spec.ports[0].port}' 2>/dev/null || true)"
TRAFFIC_PROFILE=steady \
  TRAFFIC_TARGET="http://$DEPLOY.$NS.svc.cluster.local:${SVC_PORT:-8080}/" \
  TRAFFIC_RPS="${FAULT_RPS:-20}" \
  TRAFFIC_DURATION="${FAULT_DURATION:-30m}" \
  bash "$PROJECT_ROOT/src/services/traffic/start.sh" >/dev/null 2>&1 ||
  echo "Note: traffic generator not started - run 'labctl traffic start --app $DEPLOY' to see the errors."

# Return once the kubelet has failed the probe on a pod from this change, so
# the first status check sees the fault. FAULT_WAIT_SECONDS=0 skips the wait.
probe_failed_on_new_pod() {
  kubectl -n "$NS" get events --field-selector=reason=Unhealthy \
    -o 'jsonpath={range .items[*]}{.involvedObject.name}|{.message}{"\n"}{end}' 2>/dev/null |
    grep '|Liveness probe failed' | cut -d'|' -f1 | while read -r pod; do
    case "$BEFORE" in
      *" $pod "*) ;;
      *) case "$pod" in "$DEPLOY"-*) echo "$pod" ;; esac ;;
    esac
  done
}
WAIT_SECONDS="${FAULT_WAIT_SECONDS:-90}"
waited=0
while [ "$waited" -lt "$WAIT_SECONDS" ] && [ -z "$(probe_failed_on_new_pod)" ]; do
  sleep 5
  waited=$((waited + 5))
done
echo "Done."
