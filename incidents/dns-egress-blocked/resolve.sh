#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="dns-egress-blocked"
MARK="labfault-$FAULT"
POLICY="default-deny-egress"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"

RECORDED="$(fault_state_get "$FAULT" policy)"
ORIG_UPSTREAM="$(fault_state_get "$FAULT" original-upstream)"

# The fault's policy goes, fixed or not: it is the fault's object, and an
# egress baseline left behind would break the next scenario that needs egress.
# A policy of that name with no record is someone else's and stays.
if [ -n "$RECORDED" ]; then
  echo "Removing the egress policy..."
  kubectl delete networkpolicy "$RECORDED" -n "$NS" --ignore-not-found >/dev/null
  # The fix the fault's snippet applies. Left alone it is an egress policy of
  # its own, and would allow the namespace nothing but DNS.
  kubectl delete networkpolicy allow-dns -n "$NS" --ignore-not-found >/dev/null
elif kubectl -n "$NS" get networkpolicy "$POLICY" >/dev/null 2>&1; then
  echo "No record that this fault created $POLICY in $NS - leaving it in place."
  echo "  If it is what blocks DNS, allow port 53 in it or remove it:"
  echo "    kubectl -n $NS edit networkpolicy $POLICY"
fi

# The upstream setting goes back to what it was before the fault.
if [ "$ORIG_UPSTREAM" = "none" ]; then
  kubectl -n "$NS" set env "deploy/$DEPLOY" UPSTREAM_URL- >/dev/null 2>&1 || true
elif [ -n "$ORIG_UPSTREAM" ]; then
  kubectl -n "$NS" set env "deploy/$DEPLOY" "UPSTREAM_URL=$ORIG_UPSTREAM" >/dev/null 2>&1 || true
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
