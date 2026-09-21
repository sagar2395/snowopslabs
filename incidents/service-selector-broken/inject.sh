#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
SVC="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-service-selector-broken"
# Record the WHOLE selector map, not one key's value. The workload may select on
# any label set, and resolve has to put back exactly what was there rather than
# a key this script assumed.
ORIGINAL_SELECTOR="$(kubectl -n "$NS" get svc "$SVC" \
  -o 'jsonpath={.spec.selector}' 2>/dev/null || true)"

# Guard on the fault itself, not on its bookkeeping annotation: a mark can
# outlive a resolve, and an annotation-only guard then refuses to inject on a
# lab where the Service is perfectly healthy.
POD_LABEL="$(kubectl -n "$NS" get deploy "$SVC" \
  -o 'jsonpath={.spec.template.metadata.labels.app\.kubernetes\.io/name}' 2>/dev/null || true)"
SVC_LABEL="$(kubectl -n "$NS" get svc "$SVC" \
  -o 'jsonpath={.spec.selector.app\.kubernetes\.io/name}' 2>/dev/null || true)"
if [ -n "$SVC_LABEL" ] && [ "$SVC_LABEL" != "$POD_LABEL" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

# A plausible wrong value, not one that names the lab. "labfault-nobody" sat in
# the single object the learner is meant to inspect and announced that the lab
# had done this — the whole drill is noticing that two label sets disagree, and
# a value prefixed "labfault-" answers it on sight. A half-finished rename is
# how this really happens.
BAD_LABEL="${POD_LABEL:-$SVC}-v2"

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=incidents/_lib/render.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/render.sh"
render_targeted "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

echo "Applying a configuration change to $NS/$SVC..."
kubectl -n "$NS" annotate svc "$SVC" \
  "$MARK=injected" "$MARK-original-selector=${ORIGINAL_SELECTOR:-none}" --overwrite >/dev/null
kubectl -n "$NS" patch svc "$SVC" \
  -p "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"${BAD_LABEL}\"}}}" >/dev/null

echo "Done."
