#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-crashloop-bad-config"

CMD="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].command[0]}' 2>/dev/null || true)"
ORIG_CMD="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o "jsonpath={.metadata.annotations.$MARK-original-command}" 2>/dev/null || true)"

if [ "$CMD" = "/bin/false" ]; then
  if [ -n "$ORIG_CMD" ] && [ "$ORIG_CMD" != "none" ]; then
    # Put back the command the workload actually shipped, not "no command".
    echo "Restoring $DEPLOY's original container command..."
    kubectl -n "$NS" patch deploy "$DEPLOY" --type=json \
      -p "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/command\",\"value\":$ORIG_CMD}]"
  else
    echo "Restoring $DEPLOY's container command (it had none of its own)..."
    kubectl -n "$NS" patch deploy "$DEPLOY" --type=json \
      -p '[{"op":"remove","path":"/spec/template/spec/containers/0/command"}]'
  fi
else
  echo "Container command already restored."
fi

# shellcheck source=incidents/_lib/marks.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/marks.sh"
clear_marks "$NS" "$DEPLOY" "$MARK" "$MARK-original-command"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule labfault-crashloop-bad-config -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
