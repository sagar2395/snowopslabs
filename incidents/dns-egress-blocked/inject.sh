#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="dns-egress-blocked"
POLICY="default-deny-egress"
# The dependency the app calls: echo-server, which this fault lists as a
# prerequisite, by its cluster DNS name.
UPSTREAM_NAMESPACE="${UPSTREAM_NAMESPACE:-echo-server}"
UPSTREAM_PORT="${UPSTREAM_PORT:-8080}"
UPSTREAM="http://echo-server.$UPSTREAM_NAMESPACE.svc.cluster.local:$UPSTREAM_PORT/"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

# Guard on the fault itself: a policy of this name that allows no DNS is the
# fault. One that does is somebody's working policy unless this fault made it.
EXISTING="$(kubectl -n "$NS" get networkpolicy "$POLICY" -o 'jsonpath={.spec.egress}' 2>/dev/null || true)"
if kubectl -n "$NS" get networkpolicy "$POLICY" >/dev/null 2>&1; then
  case "$EXISTING" in
    *'"port":53'*)
      if [ -z "$(fault_state_get "$FAULT" policy)" ]; then
        echo "ERROR: a NetworkPolicy named $POLICY already exists in $NS and would be overwritten." >&2
        echo "  Inject into a lab without it, or remove it first: kubectl -n $NS get networkpolicy $POLICY -o yaml" >&2
        exit 1
      fi
      ;;
    *)
      echo "Fault already injected - nothing to do."
      exit 0
      ;;
  esac
fi

UPSTREAM_NOW="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].env[?(@.name=="UPSTREAM_URL")].value}' 2>/dev/null || true)"

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator: the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

fault_state_save "$FAULT" "policy=$POLICY" "original-upstream=${UPSTREAM_NOW:-none}" \
  "upstream=${UPSTREAM_NOW:-$UPSTREAM}"

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Rolling out the security hardening to $NS..."
render_targeted "$SCRIPT_DIR/manifests/deny-egress.yaml" |
  sed -e "s|\${UPSTREAM_NAMESPACE}|$UPSTREAM_NAMESPACE|g" -e "s|\${UPSTREAM_PORT}|$UPSTREAM_PORT|g" |
  kubectl apply -n "$NS" -f - >/dev/null

# A policy governs new connections only, and the app keeps its upstream
# connection alive, so the pods turn over as at a release. The same rollout
# points an app that calls no upstream yet at echo-server.
if [ -z "$UPSTREAM_NOW" ]; then
  kubectl -n "$NS" set env "deploy/$DEPLOY" "UPSTREAM_URL=$UPSTREAM" >/dev/null
else
  kubectl -n "$NS" rollout restart "deploy/$DEPLOY" >/dev/null
fi
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s >/dev/null 2>&1 || true

# An image older than UPSTREAM_URL ignores it: nothing would call the upstream
# and the fault would never take effect. Its /ready reports an upstream field.
SELECTOR="$(workload_selector "$NS" "$DEPLOY")"
POD_PORT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].ports[0].containerPort}' 2>/dev/null || true)"
POD="$(running_pod "$NS" "$SELECTOR")"
if [ -n "$POD" ]; then
  case "$(pod_get "$NS" "$POD" "${POD_PORT:-8080}" /ready || true)" in
    *'"upstream"'*) ;;
    *)
      echo "ERROR: $NS/$DEPLOY does not call an upstream: its image predates UPSTREAM_URL." >&2
      echo "  Rebuild it, then inject again: labctl app build $DEPLOY && labctl app deploy $DEPLOY" >&2
      echo "Undoing the change..." >&2
      bash "$SCRIPT_DIR/resolve.sh" >/dev/null 2>&1 || true
      exit 1
      ;;
  esac
fi

# Requests make the failures visible on the dashboards and fire the page.
SVC_PORT="$(kubectl -n "$NS" get svc "$DEPLOY" -o 'jsonpath={.spec.ports[0].port}' 2>/dev/null || true)"
TRAFFIC_PROFILE=steady \
  TRAFFIC_TARGET="http://$DEPLOY.$NS.svc.cluster.local:${SVC_PORT:-8080}/" \
  TRAFFIC_RPS="${FAULT_RPS:-10}" \
  TRAFFIC_DURATION="${FAULT_DURATION:-30m}" \
  bash "$PROJECT_ROOT/src/services/traffic/start.sh" >/dev/null 2>&1 ||
  echo "Note: traffic generator not started - run 'labctl traffic start --app $DEPLOY' to see the errors."

# Return once a request to the app fails, so the first status check sees it.
# FAULT_WAIT_SECONDS=0 skips the wait.
WAIT_SECONDS="${FAULT_WAIT_SECONDS:-60}"
waited=0
while [ "$waited" -lt "$WAIT_SECONDS" ]; do
  POD="$(running_pod "$NS" "$SELECTOR")"
  if [ -n "$POD" ] && ! pod_get_ok "$NS" "$POD" "${POD_PORT:-8080}" "/"; then
    break
  fi
  sleep 3
  waited=$((waited + 3))
done
echo "Done."
