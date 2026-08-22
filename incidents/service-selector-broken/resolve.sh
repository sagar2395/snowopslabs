#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
SVC="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-service-selector-broken"

ORIGINAL="$(kubectl -n "$NS" get svc "$SVC" -o "jsonpath={.metadata.annotations.$MARK-original}" 2>/dev/null || true)"
if [ -z "$ORIGINAL" ]; then
  ORIGINAL="$SVC"
fi

echo "Restoring $SVC's selector (app.kubernetes.io/name=$ORIGINAL)..."
kubectl -n "$NS" patch svc "$SVC" \
  -p "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"$ORIGINAL\"}}}"

kubectl -n "$NS" annotate svc "$SVC" "$MARK-" "$MARK-original-" --overwrite >/dev/null 2>&1 || true
echo "Resolved. Endpoints repopulate within seconds."
