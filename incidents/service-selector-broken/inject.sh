#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
SVC="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-service-selector-broken"
# The demo apps' charts select on app.kubernetes.io/name=<app>.
ORIGINAL_VALUE="${SELECTOR_ORIGINAL_VALUE:-$SVC}"

if [ "$(kubectl -n "$NS" get svc "$SVC" -o "jsonpath={.metadata.annotations.$MARK}" 2>/dev/null)" = "injected" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

echo "Injecting: pointing $SVC's selector at a label no pod carries..."
kubectl -n "$NS" annotate svc "$SVC" "$MARK=injected" "$MARK-original=$ORIGINAL_VALUE" --overwrite
kubectl -n "$NS" patch svc "$SVC" \
  -p '{"spec":{"selector":{"app.kubernetes.io/name":"labfault-nobody"}}}'

echo "Injected. The Service now matches zero pods — endpoints are empty."
