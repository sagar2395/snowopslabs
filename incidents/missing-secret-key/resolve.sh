#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="missing-secret-key"
MARK="labfault-$FAULT"
LIB="$(cd "$(dirname "$0")/../_lib" && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"

ENV_NAME="$(fault_state_get "$FAULT" env)"
SECRET="$(fault_state_get "$FAULT" secret)"
KEY="$(fault_state_get "$FAULT" original-key)"
NEW_KEY="$(fault_state_get "$FAULT" new-key)"

secret_has() {
  kubectl -n "$NS" get secret "$SECRET" \
    -o 'go-template={{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}' 2>/dev/null | grep -qx "$1"
}

# "<secret>|<key>" the recorded variable reads now, or nothing.
ref_now() {
  kubectl -n "$NS" get deploy "$DEPLOY" \
    -o 'jsonpath={range .spec.template.spec.containers[0].env[*]}{.name}|{.valueFrom.secretKeyRef.name}|{.valueFrom.secretKeyRef.key}{"\n"}{end}' 2>/dev/null |
    awk -F'|' -v e="$ENV_NAME" '$1 == e && $3 != "" { print $2 "|" $3; exit }' || true
}

if [ -z "$KEY" ]; then
  echo "No record of the rotated Secret key - leaving the Secret and the Deployment as they are."
  echo "  If pods show CreateContainerConfigError, read the key they want with:"
  echo "    kubectl -n $NS describe pod <pod>"
else
  REF="$(ref_now)"
  REF_SECRET="${REF%%|*}"
  REF_KEY="${REF#*|}"
  # The learner's fix stays when the variable reads a key that exists.
  if [ -n "$REF" ] && kubectl -n "$NS" get secret "$REF_SECRET" \
    -o 'go-template={{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}' 2>/dev/null | grep -qx "$REF_KEY"; then
    echo "Secret reference already fixed."
  else
    echo "Restoring the Secret key and $DEPLOY's reference to it..."
    if ! secret_has "$KEY" && secret_has "$NEW_KEY"; then
      VALUE="$(kubectl -n "$NS" get secret "$SECRET" -o "jsonpath={.data.$NEW_KEY}")"
      kubectl -n "$NS" patch secret "$SECRET" --type=json \
        -p "[{\"op\":\"add\",\"path\":\"/data/$KEY\",\"value\":\"$VALUE\"},{\"op\":\"remove\",\"path\":\"/data/$NEW_KEY\"}]" >/dev/null
    elif ! secret_has "$KEY"; then
      echo "  Secret $SECRET has neither $KEY nor $NEW_KEY to restore from. Re-apply the app's chart:"
      echo "    labctl app deploy $DEPLOY"
    fi
    # Unchanged when the reference is already the original; otherwise it also
    # drops a hardcoded value or an optional flag put there by hand.
    CONTAINER="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].name}')"
    kubectl -n "$NS" patch deploy "$DEPLOY" --type=strategic \
      -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"$CONTAINER\",\"env\":[{\"name\":\"$ENV_NAME\",\"value\":null,\"valueFrom\":{\"secretKeyRef\":{\"name\":\"$SECRET\",\"key\":\"$KEY\",\"optional\":null}}}]}]}}}}" >/dev/null
  fi
fi

fault_state_clear "$FAULT"

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Waiting for the rollout to complete..."
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s || true
echo "Resolved."
