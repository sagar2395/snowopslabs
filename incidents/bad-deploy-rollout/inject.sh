#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-bad-deploy-rollout"

# Guard on the fault itself, not on its bookkeeping annotation. `kubectl rollout
# undo` restores a ReplicaSet's annotations onto the Deployment, so the mark can
# outlive a resolve — and an annotation-only guard then refuses to inject on a
# lab where the fault is not present at all.
case "$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].image}' 2>/dev/null)" in
  *:v99.9.9)
    echo "Fault already injected — nothing to do."
    exit 0
    ;;
esac

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
. "$(cd "$(dirname "$0")/../_lib" && pwd)/render.sh"
render_targeted "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

CURRENT_IMAGE="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].image}')"
CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}')"

# Bump the tag on the image the workload is ALREADY running, rather than
# inventing a hostname. A made-up registry fails DNS resolution, which is a
# different incident with a different fix — and "registry.invalid/never-pushed"
# also announces the answer in the pod spec. Reusing the real repository with an
# unpushed tag produces what a real bad release produces: "repository does not
# exist or may require authorization".
#
# Strip the tag, not a registry port: only the last colon segment counts, and
# only when it has no "/" after it.
REPO="$CURRENT_IMAGE"
case "${CURRENT_IMAGE##*:}" in
  */*) : ;;              # "host:5000/repo" — no tag to strip
  "$CURRENT_IMAGE") : ;; # no colon at all
  *) REPO="${CURRENT_IMAGE%:*}" ;;
esac
BAD_IMAGE="${REPO}:v99.9.9"

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Rolling out a new release of $NS/$DEPLOY..."
kubectl -n "$NS" annotate deploy "$DEPLOY" "$MARK-original=$CURRENT_IMAGE" --overwrite >/dev/null
kubectl -n "$NS" set image "deploy/$DEPLOY" "$CONTAINER=$BAD_IMAGE" >/dev/null

echo "Done. The rollout is in progress."
