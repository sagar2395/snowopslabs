#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-crashloop-bad-config"
# A fat-fingered port: one zero too many. Every demo app reads PORT at startup
# and exits with an error naming it, so the crash leaves evidence in the logs.
BAD_PORT="80800"

port_now() {
  kubectl -n "$NS" get deploy "$DEPLOY" \
    -o 'jsonpath={.spec.template.spec.containers[0].env[?(@.name=="PORT")].value}' 2>/dev/null || true
}

# Guard on the fault itself, not on its bookkeeping annotation: `kubectl
# rollout undo` can restore a stale mark onto a Deployment the fault is not in.
if [ "$(port_now)" = "$BAD_PORT" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
. "$(cd "$(dirname "$0")/../_lib" && pwd)/render.sh"
render_targeted "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

# Record the PORT the workload shipped so resolve puts back exactly that; "none"
# means the app set no PORT of its own and resolve removes the variable again.
ORIG_PORT="$(port_now)"
kubectl -n "$NS" annotate deploy "$DEPLOY" \
  "$MARK-original-port=${ORIG_PORT:-none}" "$MARK=injected" --overwrite >/dev/null

# Nothing printed here names the fault: a progress line saying which setting
# changed hands over the answer before the drill starts.
CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}')"
echo "Rolling out a config change to $NS/$DEPLOY..."
kubectl -n "$NS" set env "deploy/$DEPLOY" -c "$CONTAINER" "PORT=$BAD_PORT" >/dev/null

# Retire the previous ReplicaSets so no old pod is left serving. This is the
# state a release reaches when old pods go before new ones prove healthy (a
# Recreate strategy, or a crash that comes after readiness): an outage, not a
# stalled rollout. The ReplicaSets stay, so `kubectl rollout undo` still works.
GEN="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.metadata.generation}')"
i=0
while [ "$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.status.observedGeneration}')" != "$GEN" ] && [ "$i" -lt 30 ]; do
  i=$((i + 1))
  sleep 1
done
REV="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.metadata.annotations.deployment\.kubernetes\.io/revision}')"
kubectl -n "$NS" get rs \
  -o 'jsonpath={range .items[*]}{.metadata.name} {.metadata.ownerReferences[0].name} {.metadata.annotations.deployment\.kubernetes\.io/revision}{"\n"}{end}' |
  while read -r rs owner rev; do
    [ "$owner" = "$DEPLOY" ] && [ "$rev" != "$REV" ] || continue
    kubectl -n "$NS" scale rs "$rs" --replicas=0 >/dev/null
  done

echo "Done. The new pods are starting."
