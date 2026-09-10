#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-crashloop-bad-config"

# Guard on the fault itself, not on its bookkeeping annotation. `kubectl rollout
# undo` restores a ReplicaSet's annotations onto the Deployment, so the mark can
# outlive a resolve — and an annotation-only guard then refuses to inject on a
# lab where the fault is not present at all, with no way for the learner to
# recover except editing annotations by hand.
if [ "$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].command[0]}' 2>/dev/null)" = "/bin/false" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
. "$(cd "$(dirname "$0")/../_lib" && pwd)/render.sh"
render_targeted "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

# Save whatever command the workload had BEFORE overwriting it. A JSON-patch
# "add" on an existing key replaces it, so without this the original is gone and
# resolve can only delete the key — leaving a workload that shipped its own
# command (a JVM launcher, an entrypoint wrapper) permanently different from how
# the fault found it. Empty is recorded as "none", which resolve restores as
# "remove the key".
ORIG_CMD="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].command}' 2>/dev/null || true)"
kubectl -n "$NS" annotate deploy "$DEPLOY" \
  "$MARK-original-command=${ORIG_CMD:-none}" --overwrite >/dev/null

# Nothing printed here names the fault. The learner's brief is fault.yaml's
# description and the page this armed; a progress line saying which field was
# changed hands over the answer before the drill starts.
echo "Rolling out a change to $NS/$DEPLOY..."
kubectl -n "$NS" patch deploy "$DEPLOY" --type=json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/command","value":["/bin/false"]}]' >/dev/null
kubectl -n "$NS" annotate deploy "$DEPLOY" "$MARK=injected" --overwrite >/dev/null

echo "Done. The rollout is in progress."
