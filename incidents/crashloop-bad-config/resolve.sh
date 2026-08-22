#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-crashloop-bad-config"

CMD="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].command[0]}' 2>/dev/null || true)"
if [ "$CMD" = "/bin/false" ]; then
  echo "Restoring $DEPLOY's container command..."
  kubectl -n "$NS" patch deploy "$DEPLOY" --type=json \
    -p '[{"op":"remove","path":"/spec/template/spec/containers/0/command"}]'
else
  echo "Container command already restored."
fi

kubectl -n "$NS" annotate deploy "$DEPLOY" "$MARK-" --overwrite >/dev/null 2>&1 || true

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule labfault-crashloop-bad-config -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
