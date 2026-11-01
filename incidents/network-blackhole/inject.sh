#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="network-blackhole"
POLICY="default-deny-ingress"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"

# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"

# Guard on the fault itself: a policy of this name that admits no ingress is
# the fault, whoever applied it. One that carries rules is somebody's working
# policy unless this fault recorded creating it, and is not overwritten.
EXISTING="$(kubectl -n "$NS" get networkpolicy "$POLICY" \
  -o 'jsonpath={.spec.policyTypes}{"|"}{.spec.ingress}' 2>/dev/null || true)"
case "$EXISTING" in
  "") ;;
  *Ingress*"|" | *Ingress*"|[]")
    echo "Fault already injected - nothing to do."
    exit 0
    ;;
  *)
    if [ -z "$(fault_state_get "$FAULT" policy)" ]; then
      echo "ERROR: a NetworkPolicy named $POLICY already exists in $NS and would be overwritten." >&2
      echo "  Inject into another app, or remove that policy first:" >&2
      echo "    kubectl -n $NS get networkpolicy $POLICY -o yaml" >&2
      exit 1
    fi
    ;;
esac

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator - the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Applying a network configuration change in $NS..."
fault_state_save "$FAULT" "policy=$POLICY"
render_targeted "$SCRIPT_DIR/manifests/deny-ingress.yaml" | kubectl apply -f - >/dev/null

# A NetworkPolicy only governs NEW connections. The ingress controller keeps a
# keep-alive pool to the backend, and every one of those established connections
# has a conntrack entry that keeps flowing - so with the policy applied and the
# pods untouched the service answers 200 indefinitely, and the incident reports
# itself resolved the moment it is injected. Measured: restarting the ingress
# controller turned 200 into 502 immediately.
#
# Restarting the WORKLOAD's pods is what forces the turnover without touching a
# platform component. It is also how this presents in production: the policy
# lands quietly and the outage starts at the next restart.
echo "Waiting for existing connections to the workload to turn over..."
kubectl -n "$NS" rollout restart "deploy/$DEPLOY" >/dev/null
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s >/dev/null 2>&1 || true

echo "Done."
echo "Tip: run 'labctl traffic start --app ${TARGET_WORKLOAD:-go-api}' - the page needs requests to fail before it fires."
