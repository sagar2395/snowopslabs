#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-crashloop-bad-config"
BAD_PORT="80800"

PORT_NOW="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].env[?(@.name=="PORT")].value}' 2>/dev/null || true)"
ORIG_PORT="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o "jsonpath={.metadata.annotations.$MARK-original-port}" 2>/dev/null || true)"
CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}' 2>/dev/null || true)"

# Without the recorded original (a rollback can drop it) the container's own
# declared port is the value the app must listen on.
if [ -z "$ORIG_PORT" ] && [ "$PORT_NOW" = "$BAD_PORT" ]; then
  ORIG_PORT="$(kubectl -n "$NS" get deploy "$DEPLOY" \
    -o 'jsonpath={.spec.template.spec.containers[0].ports[0].containerPort}' 2>/dev/null || true)"
  ORIG_PORT="${ORIG_PORT:-none}"
fi

# Compare against the original rather than the fault's value, so a half-fixed
# PORT (a different wrong number, or a removed variable) is put right too.
if [ -z "$ORIG_PORT" ] || [ "$PORT_NOW" = "$ORIG_PORT" ] || { [ "$ORIG_PORT" = "none" ] && [ -z "$PORT_NOW" ]; }; then
  echo "PORT already restored."
elif [ "$ORIG_PORT" = "none" ]; then
  echo "Removing the PORT setting from $DEPLOY (it had none of its own)..."
  kubectl -n "$NS" set env "deploy/$DEPLOY" -c "$CONTAINER" PORT- >/dev/null
else
  echo "Restoring $DEPLOY's PORT setting..."
  kubectl -n "$NS" set env "deploy/$DEPLOY" -c "$CONTAINER" "PORT=$ORIG_PORT" >/dev/null
fi

# shellcheck source=incidents/_lib/marks.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/marks.sh"
clear_marks "$NS" "$DEPLOY" "$MARK" "$MARK-original-port"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule labfault-crashloop-bad-config -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
