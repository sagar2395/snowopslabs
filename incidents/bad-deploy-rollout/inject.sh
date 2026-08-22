#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-bad-deploy-rollout"
BAD_IMAGE="registry.invalid/never-pushed/go-api:v99.9.9"

ORIGINAL="$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK-original}" 2>/dev/null || true)"
if [ -n "$ORIGINAL" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
kubectl apply -n "$MON_NS" -f "$SCRIPT_DIR/alerts/rule.yaml" 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

CURRENT_IMAGE="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].image}')"
CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}')"

echo "Injecting: deploying image tag that was never pushed..."
kubectl -n "$NS" annotate deploy "$DEPLOY" "$MARK-original=$CURRENT_IMAGE" --overwrite
kubectl -n "$NS" set image "deploy/$DEPLOY" "$CONTAINER=$BAD_IMAGE"

echo "Injected. New pods will sit in ImagePullBackOff; the rollout is stuck."
