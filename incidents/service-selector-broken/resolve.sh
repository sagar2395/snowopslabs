#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
SVC="${TARGET_WORKLOAD:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="service-selector-broken"
MARK="labfault-$FAULT"
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
# shellcheck source=incidents/_lib/state.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/state.sh"

# Forget the record, disarm the page, and drop the annotations a lab may still
# carry on the Service.
clean_up() {
  fault_state_clear "$FAULT"
  kubectl -n "$NS" annotate svc "$SVC" \
    "$MARK-" "$MARK-original-selector-" --overwrite >/dev/null 2>&1 || true
  kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found >/dev/null 2>&1 || true
}

# Resolve also runs after a hand fix passes the check, so it leaves a Service
# that selects its own pods alone, however the learner got it there: putting
# the recorded selector back over a fix made on the Deployment's labels would
# break the Service again.
POD_LABEL="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.metadata.labels.app\.kubernetes\.io/name}' 2>/dev/null || true)"
SVC_LABEL="$(kubectl -n "$NS" get svc "$SVC" \
  -o 'jsonpath={.spec.selector.app\.kubernetes\.io/name}' 2>/dev/null || true)"
if [ -z "$SVC_LABEL" ] || [ "$SVC_LABEL" = "$POD_LABEL" ]; then
  clean_up
  echo "Nothing to resolve: $SVC selects its own pods."
  exit 0
fi

# Prefer the selector recorded at injection: it restores the Service exactly as
# the fault found it.
SELECTOR="$(fault_state_get "$FAULT" original-selector)"

# The Deployment's matchLabels is the fallback. It is the authoritative answer
# to "which pods is this app" and converges even when the user edited a
# different key by hand, but it is a SUBSET of what a chart usually puts on a
# Service, so restoring from it drops keys.
if [ -z "$SELECTOR" ] || [ "$SELECTOR" = "none" ] || [ "$SELECTOR" = "{}" ]; then
  SELECTOR="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.selector.matchLabels}' 2>/dev/null || true)"
fi
if [ -z "$SELECTOR" ] || [ "$SELECTOR" = "{}" ] || [ "$SELECTOR" = "none" ]; then
  echo "FAIL: cannot determine $SVC's original selector - restore it by hand:" >&2
  echo "  kubectl -n $NS get deploy $DEPLOY -o jsonpath='{.spec.selector.matchLabels}'" >&2
  exit 1
fi

# REPLACE the selector, do not merge into it. A strategic-merge patch only
# overwrites the keys it names, so the injected key survives alongside the
# restored ones - and a selector is an AND, so the Service still matches zero
# pods while resolve reports success. Only visible when the workload selects on
# a different label key than the one injected.
echo "Restoring $SVC's selector..."
kubectl -n "$NS" patch svc "$SVC" --type=json \
  -p "[{\"op\":\"replace\",\"path\":\"/spec/selector\",\"value\":$SELECTOR}]"

clean_up

echo "Resolved. Endpoints repopulate within seconds."
