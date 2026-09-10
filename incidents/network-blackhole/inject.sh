#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"

# shellcheck source=/dev/null
. "$(cd "$SCRIPT_DIR/../_lib" && pwd)/render.sh"

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
render_targeted "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Applying a network configuration change in $NS..."
render_targeted "$SCRIPT_DIR/manifests/deny-ingress.yaml" | kubectl apply -f - >/dev/null

# A NetworkPolicy only governs NEW connections. The ingress controller keeps a
# keep-alive pool to the backend, and every one of those established connections
# has a conntrack entry that keeps flowing — so with the policy applied and the
# pods untouched the service answers 200 indefinitely, and the incident reports
# itself resolved the moment it is injected. Measured: restarting the ingress
# controller turned 200 into 502 immediately.
#
# Restarting the WORKLOAD's pods is what forces the turnover without touching a
# platform component. It is also how this presents in production: the policy
# lands quietly and the outage starts at the next restart.
echo "Waiting for existing connections to the workload to turn over..."
kubectl -n "$NS" rollout restart "deploy/$DEPLOY" >/dev/null
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=120s >/dev/null 2>&1 || true

echo "Done."
echo "Tip: run 'labctl traffic start' — the page needs requests to fail before it fires."
