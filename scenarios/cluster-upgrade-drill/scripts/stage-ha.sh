#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

# stage-ha.sh — put the workload into the shape the drill needs: enough replicas that
# a PodDisruptionBudget has something to protect. Staging only; the learner
# still writes the PDB and performs the roll.

APP_NAMESPACE="${WORKLOAD_NAMESPACE}"
REPLICAS="${UPGRADE_DRILL_REPLICAS:-3}"

WORKLOAD="${WORKLOAD_NAME}"
STATE="upgrade-drill-baseline"

# Record the cluster as the drill found it.
#
# "No node left behind" cannot be answered by looking at the cluster alone: a
# cluster nobody has touched has every node on one version, which is exactly what
# a finished roll looks like.
#
# Recorded by UID rather than by name. k3d reuses a node's name whenever it can,
# so a rolled worker frequently comes back called exactly what it was — a name
# comparison would report it as never rolled. A recreated node always has a new
# UID.
WORKER_UIDS="$(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' \
  -o jsonpath='{range .items[*]}{.metadata.uid}{" "}{end}' 2>/dev/null || true)"
WORKERS="$(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{end}' 2>/dev/null || true)"
VERSIONS="$(kubectl get nodes -o jsonpath='{range .items[*]}{.status.nodeInfo.kubeletVersion}{"\n"}{end}' \
  2>/dev/null | sort -u | tr '\n' ' ' || true)"
BEFORE="$(kubectl -n "$APP_NAMESPACE" get deployment "$WORKLOAD" \
  -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 1)"

# Never overwrite an existing checkpoint. A re-activation with --force runs over
# a cluster this scenario has ALREADY staged, so re-recording would capture the
# staged state as the baseline — and teardown would then "restore" the workload
# to the replica count the drill itself set, never to what it actually found.
if kubectl -n "$APP_NAMESPACE" get configmap "$STATE" >/dev/null 2>&1; then
  echo "==> Keeping the checkpoint from the first activation (re-run detected)."
else
  kubectl -n "$APP_NAMESPACE" create configmap "$STATE" \
    --from-literal=workers="$WORKERS" \
    --from-literal=worker-uids="$WORKER_UIDS" \
    --from-literal=versions="$VERSIONS" \
    --from-literal=replicas-before="${BEFORE:-1}" \
    --from-literal=staged-at="$(date -u +%s)" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  echo "==> Recorded the starting cluster: workers [${WORKERS}] on ${VERSIONS}"
fi

# A pod covered by two PodDisruptionBudgets cannot be evicted at all — the
# eviction subresource refuses it, and the Kubernetes error names neither budget.
# Warn now rather than let the learner meet it halfway through a drain.
OTHER="$(kubectl -n "$APP_NAMESPACE" get pdb --no-headers \
  -o 'custom-columns=NAME:.metadata.name,APP:.spec.selector.matchLabels.app' 2>/dev/null |
  awk -v app="$WORKLOAD" -v mine="${WORKLOAD}-upgrade-pdb" '$2 == app && $1 != mine { print $1 }' || true)"
if [ -n "$OTHER" ]; then
  echo "WARNING: another PodDisruptionBudget already selects ${WORKLOAD}:"
  printf '  %s\n' $OTHER
  echo "  A pod covered by two budgets cannot be evicted AT ALL, so your drain will"
  echo "  fail partway through with a message naming neither of them. That budget"
  echo "  belongs to another scenario — tear that one down first."
fi

echo "==> Scaling ${WORKLOAD} to ${REPLICAS} replicas for the upgrade drill"
kubectl -n "$APP_NAMESPACE" scale deployment "$WORKLOAD" --replicas="$REPLICAS"

# Only wait for the HA floor the drill actually needs. Insisting on all
# replicas would fail on a lab with fewer schedulable nodes than replicas.
echo "==> Waiting for at least 2 ready replicas"
for _ in $(seq 1 60); do
  ready="$(kubectl -n "$APP_NAMESPACE" get deployment ${WORKLOAD_NAME} \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)"
  if [ "${ready:-0}" -ge 2 ]; then
    echo "${WORKLOAD_NAME} has ${ready} ready replicas — staged."
    exit 0
  fi
  sleep 5
done

echo "ERROR: ${WORKLOAD_NAME} did not reach 2 ready replicas in 300s." >&2
kubectl -n "$APP_NAMESPACE" get pods -o wide >&2
exit 1
