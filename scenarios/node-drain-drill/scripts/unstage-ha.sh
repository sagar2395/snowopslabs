#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

# unstage-ha.sh — undo stage-ha.sh, remove the PodDisruptionBudget the learner
# applied, and return any node they left cordoned. The engine only tracks what
# it staged, so without this a `down` leaves the budget behind and the next run
# starts with its first check already green.

APP_NAMESPACE="${WORKLOAD_NAMESPACE}"
WORKLOAD="${WORKLOAD_NAME}"

kubectl -n "$APP_NAMESPACE" delete pdb "${WORKLOAD}-drill-pdb" --ignore-not-found
kubectl -n "$APP_NAMESPACE" delete configmap "${WORKLOAD}-drain-drill-baseline" --ignore-not-found

# A cordoned node outlives the scenario and silently starves every later drill
# of capacity, so teardown must not leave one behind.
CORDONED="$(kubectl get nodes \
  -o jsonpath='{range .items[?(@.spec.unschedulable==true)]}{.metadata.name}{"\n"}{end}' \
  2>/dev/null || true)"
for node in $CORDONED; do
  echo "Uncordoning ${node}, left cordoned by the drill."
  kubectl uncordon "$node" >/dev/null 2>&1 || true
done

kubectl -n "$APP_NAMESPACE" scale deployment "$WORKLOAD" --replicas=1 >/dev/null 2>&1 || true
echo "Removed the drill PDB and scaled ${WORKLOAD} back to 1 replica."
