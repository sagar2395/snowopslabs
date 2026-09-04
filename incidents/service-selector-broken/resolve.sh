#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
SVC="${TARGET_WORKLOAD:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-service-selector-broken"

# The Deployment's matchLabels is the authoritative answer to "which pods is
# this app", so restoring from it converges even when the user edited a
# different selector key by hand while diagnosing.
SELECTOR="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.selector.matchLabels}' 2>/dev/null || true)"
if [ -z "$SELECTOR" ] || [ "$SELECTOR" = "{}" ]; then
  ORIGINAL="$(kubectl -n "$NS" get svc "$SVC" -o "jsonpath={.metadata.annotations.$MARK-original}" 2>/dev/null || true)"
  SELECTOR="{\"app.kubernetes.io/name\":\"${ORIGINAL:-$SVC}\"}"
fi

echo "Restoring $SVC's selector to the Deployment's own pod labels..."
kubectl -n "$NS" patch svc "$SVC" -p "{\"spec\":{\"selector\":$SELECTOR}}"

kubectl -n "$NS" annotate svc "$SVC" "$MARK-" "$MARK-original-" --overwrite >/dev/null 2>&1 || true
echo "Resolved. Endpoints repopulate within seconds."
