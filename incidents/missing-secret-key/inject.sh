#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="missing-secret-key"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$(cd "$SCRIPT_DIR/../_lib" && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"
# shellcheck source=incidents/_lib/workload.sh
. "$LIB/workload.sh"

# The first environment variable the container reads from a Secret key, as
# "<env>|<secret>|<key>".
REF="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={range .spec.template.spec.containers[0].env[*]}{.name}|{.valueFrom.secretKeyRef.name}|{.valueFrom.secretKeyRef.key}{"\n"}{end}' 2>/dev/null |
  awk -F'|' '$2 != "" && $3 != "" { print; exit }' || true)"
ENV_NAME="${REF%%|*}"
SECRET="$(printf '%s' "$REF" | cut -d'|' -f2)"
KEY="$(printf '%s' "$REF" | cut -d'|' -f3)"

secret_keys() {
  kubectl -n "$NS" get secret "$1" \
    -o 'go-template={{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}' 2>/dev/null || true
}

# Guard on the fault itself: the recorded rename is still in the Secret.
NEW_KEY="$(fault_state_get "$FAULT" new-key)"
if [ -n "$NEW_KEY" ] && [ -n "$SECRET" ] &&
  ! secret_keys "$SECRET" | grep -qx "$KEY" && secret_keys "$SECRET" | grep -qx "$NEW_KEY"; then
  echo "Fault already injected - nothing to do."
  exit 0
fi

if [ -z "$REF" ]; then
  if [ -n "$(kubectl get nodes -o name 2>/dev/null || true)" ]; then
    echo "ERROR: $NS/$DEPLOY reads no environment variable from a Secret key, so there is nothing to rotate." >&2
    echo "  go-api's chart reads API_TOKEN from its own Secret. Redeploy it with: labctl app deploy go-api" >&2
    exit 1
  fi
  echo "No cluster reachable - staging the change without a Secret to rotate."
  ENV_NAME=API_TOKEN SECRET="$DEPLOY-secret" KEY=api-token
fi

# The rotation writes the value under the variable-style name of the key.
NEW_KEY="$(printf '%s' "$KEY" | tr '[:lower:]' '[:upper:]' | tr '.-' '__')"
[ "$NEW_KEY" != "$KEY" ] || NEW_KEY="${KEY}_V2"
VALUE="$(kubectl -n "$NS" get secret "$SECRET" -o "jsonpath={.data.$KEY}" 2>/dev/null || true)"

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator: the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

fault_state_save "$FAULT" "env=$ENV_NAME" "secret=$SECRET" "original-key=$KEY" "new-key=$NEW_KEY"

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Running the overnight maintenance on $NS..."
kubectl -n "$NS" patch secret "$SECRET" --type=json \
  -p "[{\"op\":\"add\",\"path\":\"/data/$NEW_KEY\",\"value\":\"$VALUE\"},{\"op\":\"remove\",\"path\":\"/data/$KEY\"}]" >/dev/null

# A running container keeps the environment it started with, so nothing breaks
# until the pods are replaced, as a node rotation replaces them.
SELECTOR="$(workload_selector "$NS" "$DEPLOY")"
kubectl -n "$NS" delete pod -l "$SELECTOR" --wait=false >/dev/null

# Return once a replacement pod is stuck, so the first status check sees it.
# FAULT_WAIT_SECONDS=0 skips the wait.
WAIT_SECONDS="${FAULT_WAIT_SECONDS:-60}"
waited=0
while [ "$waited" -lt "$WAIT_SECONDS" ]; do
  if kubectl -n "$NS" get pods -l "$SELECTOR" \
    -o 'jsonpath={.items[*].status.containerStatuses[*].state.waiting.reason}' 2>/dev/null |
    grep -q CreateContainerConfigError; then
    break
  fi
  sleep 2
  waited=$((waited + 2))
done
echo "Done."
