#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
SVC="${TARGET_WORKLOAD:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-service-selector-broken"

# Resolve is idempotent: with no marker there is nothing to undo, and saying so
# is the honest answer. Failing here instead would make `incident resolve` error
# on a healthy Service.
if ! kubectl -n "$NS" get svc "$SVC" \
  -o "jsonpath={.metadata.annotations.$MARK}" 2>/dev/null | grep -q .; then
  echo "Nothing to resolve: $SVC carries no $MARK marker."
  exit 0
fi

# The Deployment's matchLabels is the authoritative answer to "which pods is
# this app", so restoring from it converges even when the user edited a
# different selector key by hand while diagnosing.
# Prefer the selector recorded at injection: it restores the Service exactly as
# the fault found it. The Deployment's matchLabels is the fallback — it is the
# authoritative answer to "which pods is this app" and converges even when the
# user edited a different key by hand, but it is a SUBSET of what a chart
# usually puts on a Service, so restoring from it silently drops keys.
SELECTOR="$(kubectl -n "$NS" get svc "$SVC" \
  -o "jsonpath={.metadata.annotations.$MARK-original-selector}" 2>/dev/null || true)"
if [ -z "$SELECTOR" ] || [ "$SELECTOR" = "none" ] || [ "$SELECTOR" = "{}" ]; then
  SELECTOR="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.selector.matchLabels}' 2>/dev/null || true)"
fi
if [ -z "$SELECTOR" ] || [ "$SELECTOR" = "{}" ] || [ "$SELECTOR" = "none" ]; then
  echo "FAIL: cannot determine $SVC's original selector — restore it by hand:" >&2
  echo "  kubectl -n $NS get deploy $DEPLOY -o jsonpath='{.spec.selector.matchLabels}'" >&2
  exit 1
fi

# REPLACE the selector, do not merge into it. A strategic-merge patch only
# overwrites the keys it names, so the injected key survives alongside the
# restored ones — and a selector is an AND, so the Service still matches zero
# pods while resolve reports success. Only visible when the workload selects on
# a different label key than the one injected.
echo "Restoring $SVC's selector..."
kubectl -n "$NS" patch svc "$SVC" --type=json \
  -p "[{\"op\":\"replace\",\"path\":\"/spec/selector\",\"value\":$SELECTOR}]"

kubectl -n "$NS" annotate svc "$SVC" \
  "$MARK-" "$MARK-original-selector-" --overwrite >/dev/null 2>&1 || true
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule labfault-service-selector-broken -n "$MON_NS" --ignore-not-found >/dev/null 2>&1 || true

echo "Resolved. Endpoints repopulate within seconds."
