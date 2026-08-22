#!/usr/bin/env bash
set -euo pipefail

# drain.sh — cordon and drain one worker node under load, then uncordon it.
#
# This is the operation a real on-call engineer performs for node maintenance
# (kernel patch, hardware swap, scale-down). The PodDisruptionBudget applied by
# the scenario baseline keeps go-api available while the node's pods are evicted
# and rescheduled elsewhere. The scenario's promql check grades the request
# success rate through the operation.
#
# Env:
#   NODE          node to drain (default: a non-control-plane node)
#   DRAIN_TIMEOUT kubectl drain timeout (default: 120s)
#   APP_NAMESPACE go-api namespace (default: go-api)

DRAIN_TIMEOUT="${DRAIN_TIMEOUT:-120s}"
APP_NAMESPACE="${APP_NAMESPACE:-go-api}"

# Ensure go-api has enough replicas for the PDB to protect availability.
echo "==> Ensuring go-api has HA replicas (>=3) before the drain"
kubectl -n "$APP_NAMESPACE" scale deployment go-api --replicas=3
kubectl -n "$APP_NAMESPACE" rollout status deployment go-api --timeout=120s

# Pick a worker node to drain: prefer a node WITHOUT the control-plane role so
# we never evict the API server. Fall back to any node if roles are unlabeled.
pick_node() {
  local n
  n=$(kubectl get nodes \
    -l '!node-role.kubernetes.io/control-plane,!node-role.kubernetes.io/master' \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [ -z "$n" ]; then
    n=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
  fi
  echo "$n"
}

NODE="${NODE:-$(pick_node)}"
if [ -z "$NODE" ]; then
  echo "ERROR: could not determine a node to drain" >&2
  exit 1
fi

NODE_COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$NODE_COUNT" -lt 2 ]; then
  echo "ERROR: only ${NODE_COUNT} node(s) in the cluster — a drain has nowhere" >&2
  echo "       to reschedule pods. Recreate the cluster with >=2 agents:" >&2
  echo "       AGENTS=2 labctl runtime up" >&2
  exit 1
fi

# Always uncordon on exit so the cluster never gets stuck SchedulingDisabled,
# even if the drain fails or the script is interrupted.
cleanup() {
  echo "==> Uncordoning ${NODE}"
  kubectl uncordon "$NODE" || true
}
trap cleanup EXIT

echo "==> Cordoning ${NODE} (no new pods scheduled here)"
kubectl cordon "$NODE"

echo "==> Draining ${NODE} (evicting pods, respecting PodDisruptionBudgets)"
kubectl drain "$NODE" \
  --ignore-daemonsets \
  --delete-emptydir-data \
  --timeout="$DRAIN_TIMEOUT"

echo "==> Waiting for go-api to settle on the remaining nodes"
kubectl -n "$APP_NAMESPACE" rollout status deployment go-api --timeout=120s

echo ""
echo "Drain complete. The node will be uncordoned now."
echo "Run 'labctl scenario verify node-drain-drill' to grade availability."
