#!/usr/bin/env bash
set -euo pipefail

# stage-ha.sh — put the workload into the shape the drill needs: enough replicas
# that a PodDisruptionBudget has something to protect.
#
# Staging only. The PDB and the drain itself are the learner's work: a drill that
# applies the budget and runs the drain for you teaches neither.

APP_NAMESPACE="${APP_NAMESPACE:-${WORKLOAD_NAMESPACE:-go-api}}"
WORKLOAD="${WORKLOAD_NAME:-go-api}"
REPLICAS="${DRAIN_DRILL_REPLICAS:-3}"

# Surface a colliding budget at activation, not thirty minutes later when the
# drain fails on it. Only a warning: the learner may not have applied theirs yet,
# and the graded check is what enforces it.
EXISTING="$(kubectl -n "$APP_NAMESPACE" get pdb --no-headers \
  -o 'custom-columns=NAME:.metadata.name,APP:.spec.selector.matchLabels.app' 2>/dev/null \
  | awk -v app="$WORKLOAD" '$2 == app { print $1 }' || true)"
if [ -n "$EXISTING" ]; then
  echo "WARNING: a PodDisruptionBudget already selects ${WORKLOAD}:"
  printf '  %s\n' $EXISTING
  echo "  A pod covered by two budgets cannot be evicted, so the drain would fail."
  echo "  Deactivate the scenario that owns it before running this drill."
fi

echo "==> Scaling ${WORKLOAD} to ${REPLICAS} replicas for the drain drill"
kubectl -n "$APP_NAMESPACE" scale deployment "$WORKLOAD" --replicas="$REPLICAS"

# Only wait for the HA floor the drill actually needs. Insisting on all replicas
# would fail on a lab with fewer schedulable nodes than replicas.
# Record which nodes hold the workload before the drain.
#
# The drill is graded on pods having moved, read from the cluster — not from a
# metric. Draining a node takes Prometheus down with it whenever that node holds
# its local-path volume, so a Prometheus-based "was a node cordoned" check has a
# hole exactly where the drill happens: the observer is evicted by the event it
# is meant to observe.
record_baseline() {
  nodes="$(kubectl -n "$APP_NAMESPACE" get pods -l "app=${WORKLOAD}" \
    --field-selector=status.phase=Running \
    -o 'jsonpath={range .items[*]}{.spec.nodeName}{"\n"}{end}' 2>/dev/null | sort -u | tr '\n' ' ')"
  kubectl -n "$APP_NAMESPACE" create configmap "${WORKLOAD}-drain-drill-baseline" \
    --from-literal=nodes="$nodes" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  echo "==> Recorded pre-drain placement: ${nodes}"
}

echo "==> Waiting for at least 2 ready replicas"
for _ in $(seq 1 60); do
  ready="$(kubectl -n "$APP_NAMESPACE" get deployment "$WORKLOAD" \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)"
  if [ "${ready:-0}" -ge 2 ]; then
    echo "${WORKLOAD} has ${ready} ready replicas — staged."
    record_baseline
    exit 0
  fi
  sleep 5
done

echo "ERROR: ${WORKLOAD} did not reach 2 ready replicas in 300s." >&2
kubectl -n "$APP_NAMESPACE" get pods -o wide >&2
exit 1
