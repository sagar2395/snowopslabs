#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-bad-deploy-rollout"

ORIGINAL="$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK-original}" 2>/dev/null || true)"
if [ -n "$ORIGINAL" ]; then
  CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}')"
  echo "Restoring image $ORIGINAL..."
  kubectl -n "$NS" set image "deploy/$DEPLOY" "$CONTAINER=$ORIGINAL"
else
  echo "Image already restored (no original recorded)."
fi

kubectl -n "$NS" annotate deploy "$DEPLOY" "$MARK-original-" --overwrite >/dev/null 2>&1 || true

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule labfault-bad-deploy-rollout -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
