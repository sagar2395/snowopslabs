#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
FAULT="network-blackhole"
POLICY="default-deny-ingress"
# shellcheck source=incidents/_lib/state.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/state.sh"

echo "Removing the network policy and disarming its alert..."

# Only a policy this fault recorded creating is removed. A deny baseline of the
# same name can be the lab owner's own, kept working by allow policies beside
# it, and deleting that would open their namespace.
RECORDED="$(fault_state_get "$FAULT" policy)"
if [ -n "$RECORDED" ]; then
  kubectl delete networkpolicy "$RECORDED" -n "$NS" --ignore-not-found
elif kubectl -n "$NS" get networkpolicy "$POLICY" >/dev/null 2>&1; then
  echo "No record that this fault created $POLICY in $NS - leaving it in place."
  echo "  If it is what blocks the traffic, remove it:"
  echo "    kubectl -n $NS delete networkpolicy $POLICY"
fi
# Remove the policy under the name a lab injected by an older labctl carries.
kubectl delete networkpolicy "labfault-$FAULT" -n "$NS" --ignore-not-found >/dev/null 2>&1 || true

fault_state_clear "$FAULT"
kubectl delete prometheusrule "labfault-$FAULT" labfault-silent -n "$MON_NS" --ignore-not-found >/dev/null 2>&1 || true

# Removing the policy lets new connections through immediately, but the ingress
# controller may still be holding failed ones. Wait for the backend to settle
# rather than leaving the learner staring at 502s for a fixed policy.
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=60s >/dev/null 2>&1 || true

echo "Resolved. Traffic should flow again within a few seconds."
