#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="bad-deploy-rollout"
LIB="$(cd "$(dirname "$0")/../_lib" && pwd)"

# Guard on the fault itself, not on its record: a record can outlive a hand
# fix, and a guard on it then refuses to inject on a healthy Deployment.
case "$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].image}' 2>/dev/null)" in
  *:v99.9.9)
    echo "Fault already injected - nothing to do."
    exit 0
    ;;
esac

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator - the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

CURRENT_IMAGE="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].image}')"
CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}')"

# Bump the tag on the image the workload is ALREADY running, rather than
# inventing a hostname. A made-up registry fails DNS resolution, which is a
# different incident with a different fix - and "registry.invalid/never-pushed"
# also announces the answer in the pod spec. Reusing the real repository with an
# unpushed tag produces what a real bad release produces: "repository does not
# exist or may require authorization".
#
# Strip the tag, not a registry port: only the last colon segment counts, and
# only when it has no "/" after it.
REPO="$CURRENT_IMAGE"
case "${CURRENT_IMAGE##*:}" in
  */*) : ;;              # "host:5000/repo" - no tag to strip
  "$CURRENT_IMAGE") : ;; # no colon at all
  *) REPO="${CURRENT_IMAGE%:*}" ;;
esac
BAD_IMAGE="${REPO}:v99.9.9"

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Rolling out a new release of $NS/$DEPLOY..."
fault_state_save "$FAULT" "original-image=$CURRENT_IMAGE"
kubectl -n "$NS" set image "deploy/$DEPLOY" "$CONTAINER=$BAD_IMAGE" >/dev/null

echo "Done. The rollout is in progress."
