#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-bad-deploy-rollout"

ORIGINAL="$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK-original}" 2>/dev/null || true)"
CURRENT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].image}' 2>/dev/null || true)"

# The recorded original can legitimately be gone while the bad image is still
# deployed: `kubectl rollout undo` puts an earlier revision's pod template back,
# and if that revision is the broken one the fault returns without its
# bookkeeping. Fall back to what a person would do — read the image off the pod
# that is still serving — rather than reporting success over a broken workload.
if [ -z "$ORIGINAL" ]; then
  case "$CURRENT" in
    *:v99.9.9)
      ORIGINAL="$(kubectl -n "$NS" get pods -l "app.kubernetes.io/name=$DEPLOY" \
        --field-selector=status.phase=Running \
        -o 'jsonpath={range .items[*]}{.status.containerStatuses[0].ready}{" "}{.spec.containers[0].image}{"\n"}{end}' \
        2>/dev/null | awk '$1 == "true" && $2 !~ /:v99\.9\.9$/ { print $2; exit }')"
      [ -z "$ORIGINAL" ] || echo "No original recorded — recovering the image from the pod still serving."
      ;;
  esac
fi

if [ -n "$ORIGINAL" ]; then
  CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}')"
  echo "Restoring image $ORIGINAL..."
  kubectl -n "$NS" set image "deploy/$DEPLOY" "$CONTAINER=$ORIGINAL"
else
  case "$CURRENT" in
    *:v99.9.9)
      echo "WARNING: $NS/$DEPLOY still references $CURRENT and nothing records what it" >&2
      echo "  replaced, so this cannot put it back. Recover with:" >&2
      echo "    kubectl -n $NS rollout undo deploy/$DEPLOY" >&2
      ;;
    *) echo "Image already restored (no original recorded)." ;;
  esac
fi

# shellcheck source=incidents/_lib/marks.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/marks.sh"
clear_marks "$NS" "$DEPLOY" "$MARK" "$MARK-original"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule labfault-bad-deploy-rollout -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
