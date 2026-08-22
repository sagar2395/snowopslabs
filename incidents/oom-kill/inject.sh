#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-echo-server}"
DEPLOY="${TARGET_WORKLOAD:-echo-server}"
MARK="labfault-oom-kill"

if [ "$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK}" 2>/dev/null)" = "injected" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
kubectl apply -n "$MON_NS" -f "$SCRIPT_DIR/alerts/rule.yaml" 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

ORIG_LIMIT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true)"
ORIG_REQUEST="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || true)"

echo "Injecting: slashing $DEPLOY's memory limit to 16Mi..."
kubectl -n "$NS" annotate deploy "$DEPLOY" \
  "$MARK=injected" \
  "$MARK-original-limit=${ORIG_LIMIT:-none}" \
  "$MARK-original-request=${ORIG_REQUEST:-none}" \
  --overwrite
kubectl -n "$NS" set resources "deploy/$DEPLOY" --limits=memory=16Mi --requests=memory=8Mi

echo "Injected. Pods will be OOMKilled as soon as the runtime allocates memory."
