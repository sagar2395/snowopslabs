#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
FAULT="stray-pod-behind-service"
MARK="labfault-$FAULT"
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=incidents/_lib/state.sh
. "$(cd "$(dirname "$0")/../_lib" && pwd)/state.sh"

POD="$(fault_state_get "$FAULT" pod)"
[ -n "$POD" ] || POD="${DEPLOY}-debug"

echo "Removing the diagnostic pod..."
kubectl -n "$NS" delete pod "$POD" --ignore-not-found >/dev/null 2>&1 || true
fault_state_clear "$FAULT"
kubectl delete prometheusrule "$MARK" labfault-silent -n "$MON_NS" --ignore-not-found >/dev/null 2>&1 || true

echo "Stopping the traffic generator this fault started..."
bash "$PROJECT_ROOT/src/services/traffic/stop.sh" >/dev/null 2>&1 || true
echo "Resolved."
