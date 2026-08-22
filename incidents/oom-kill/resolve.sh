#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-echo-server}"
DEPLOY="${TARGET_WORKLOAD:-echo-server}"
MARK="labfault-oom-kill"

ORIG_LIMIT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK-original-limit}" 2>/dev/null || true)"
ORIG_REQUEST="$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK-original-request}" 2>/dev/null || true)"

# Sensible lab defaults when the deployment had no explicit memory settings.
if [ -z "$ORIG_LIMIT" ] || [ "$ORIG_LIMIT" = "none" ]; then
  ORIG_LIMIT="256Mi"
fi
if [ -z "$ORIG_REQUEST" ] || [ "$ORIG_REQUEST" = "none" ]; then
  ORIG_REQUEST="64Mi"
fi

echo "Restoring memory settings (limit=$ORIG_LIMIT, request=$ORIG_REQUEST)..."
kubectl -n "$NS" set resources "deploy/$DEPLOY" \
  --limits="memory=$ORIG_LIMIT" --requests="memory=$ORIG_REQUEST"

kubectl -n "$NS" annotate deploy "$DEPLOY" \
  "$MARK-" "$MARK-original-limit-" "$MARK-original-request-" \
  --overwrite >/dev/null 2>&1 || true

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule labfault-oom-kill -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
