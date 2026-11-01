#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="bad-deploy-rollout"
MARK="labfault-$FAULT"
LIB="$(cd "$(dirname "$0")/../_lib" && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"

ORIGINAL="$(fault_state_get "$FAULT" original-image)"
CURRENT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].image}' 2>/dev/null || true)"

# With nothing recorded and the bad image still deployed, fall back to what a
# person would do - read the image off the pod that is still serving - rather
# than reporting success over a broken workload.
if [ -z "$ORIGINAL" ]; then
  case "$CURRENT" in
    *:v99.9.9)
      ORIGINAL="$(kubectl -n "$NS" get pods -l "app.kubernetes.io/name=$DEPLOY" \
        --field-selector=status.phase=Running \
        -o 'jsonpath={range .items[*]}{.status.containerStatuses[0].ready}{" "}{.spec.containers[0].image}{"\n"}{end}' \
        2>/dev/null | awk '$1 == "true" && $2 !~ /:v99\.9\.9$/ { print $2; exit }')"
      [ -z "$ORIGINAL" ] || echo "No original recorded - recovering the image from the pod still serving."
      ;;
  esac
fi

# Resolve also runs after a hand fix passes the check, so an image that is not
# the fault's is left alone: the learner may have shipped a newer good release.
case "$CURRENT" in
  *:v99.9.9) ;;
  *) ORIGINAL="" ;;
esac

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
    *) echo "Image already restored." ;;
  esac
fi

fault_state_clear "$FAULT"
# Drop the annotations a lab may still carry on the Deployment.
# shellcheck source=incidents/_lib/marks.sh
. "$LIB/marks.sh"
clear_marks "$NS" "$DEPLOY" "$MARK" "$MARK-original"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
