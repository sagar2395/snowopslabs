#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="stray-pod-behind-service"
POD="${DEPLOY}-debug"
BAD_UPSTREAM="http://no-such-upstream.invalid:8080"
LIB="$(cd "$(dirname "$0")/../_lib" && pwd)"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$LIB/state.sh"
# shellcheck source=incidents/_lib/render.sh
. "$LIB/render.sh"

if [ -n "$(kubectl -n "$NS" get pod "$POD" -o name 2>/dev/null || true)" ]; then
  if [ -n "$(fault_state_get "$FAULT" pod)" ]; then
    echo "Fault already injected - nothing to do."
    exit 0
  fi
  echo "ERROR: pod $NS/$POD already exists and is not recorded by this fault." >&2
  echo "  Choose another lab or remove that pod before injecting." >&2
  exit 1
fi

IMAGE="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].image}' 2>/dev/null || true)"
LABELS="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'go-template={{range $k, $v := .spec.template.metadata.labels}}{{$k}}={{$v}},{{end}}' 2>/dev/null |
  sed 's/,$//' || true)"

# The Service may target the port by name, so the pod declares the same name
# and number as the Deployment's first container port.
PORT_SPEC="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'go-template={{with index (index .spec.template.spec.containers 0).ports 0}}{{.name}} {{.containerPort}}{{end}}' 2>/dev/null || true)"
PORT_NAME="${PORT_SPEC%% *}"
PORT_NUMBER="${PORT_SPEC##* }"

if [ -z "$IMAGE" ] || [ -z "$LABELS" ] || [ -z "$PORT_NUMBER" ]; then
  if [ -n "$(kubectl get nodes -o name 2>/dev/null || true)" ]; then
    echo "ERROR: could not read the image, labels and container port from deployment $NS/$DEPLOY." >&2
    echo "  Redeploy it with: labctl app deploy $DEPLOY" >&2
    exit 1
  fi
  echo "No cluster reachable - staging the change with the chart's defaults."
  IMAGE="${IMAGE:-$DEPLOY:latest}"
  LABELS="${LABELS:-app=$DEPLOY}"
  PORT_NAME=http
  PORT_NUMBER=8080
fi
if [ -n "$PORT_NAME" ]; then
  PORT_JSON="{\"name\":\"$PORT_NAME\",\"containerPort\":$PORT_NUMBER}"
else
  PORT_JSON="{\"containerPort\":$PORT_NUMBER}"
fi

MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_rule "$SCRIPT_DIR/alerts/rule.yaml" |
  kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) - continuing without paging."

fault_state_save "$FAULT" "pod=$POD"

echo "Injecting the fault into $NS..."
kubectl -n "$NS" run "$POD" \
  --image="$IMAGE" \
  --image-pull-policy=IfNotPresent \
  --restart=Never \
  --labels="$LABELS" \
  --env="UPSTREAM_URL=$BAD_UPSTREAM" \
  --override-type=strategic \
  --overrides="{\"spec\":{\"containers\":[{\"name\":\"$POD\",\"ports\":[$PORT_JSON]}]}}" >/dev/null

# A bare pod has no readiness probe, so Running is its Ready signal. Wait for
# it before starting traffic, which keeps the failure fraction stable.
kubectl -n "$NS" wait --for=condition=Ready "pod/$POD" --timeout=60s >/dev/null 2>&1 || true

TRAFFIC_PROFILE=steady \
  TRAFFIC_TARGET="http://$DEPLOY.$NS.svc.cluster.local:8080/" \
  TRAFFIC_RPS="${FAULT_RPS:-20}" \
  TRAFFIC_DURATION="${FAULT_DURATION:-30m}" \
  bash "$PROJECT_ROOT/src/services/traffic/start.sh" >/dev/null 2>&1 ||
  echo "Note: traffic generator not started - run 'labctl traffic start --app $DEPLOY' to see the errors."

sleep "${FAULT_WAIT_SECONDS:-30}"
echo "Done."
