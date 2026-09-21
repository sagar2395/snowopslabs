#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

# unstage-ha.sh — undo stage-ha.sh and remove the PodDisruptionBudget the
# learner applied, so 'scenario down' leaves nothing behind. The workload itself is an
# app prerequisite and stays running.

APP_NAMESPACE="${WORKLOAD_NAMESPACE}"

WORKLOAD="${WORKLOAD_NAME}"
STATE="upgrade-drill-baseline"

kubectl -n "$APP_NAMESPACE" delete pdb "${WORKLOAD}-upgrade-pdb" --ignore-not-found

# Restore the replica count the drill found rather than assuming 1: the workload
# is shared, and another scenario may legitimately have scaled it.
BEFORE="$(kubectl -n "$APP_NAMESPACE" get configmap "$STATE" \
  -o jsonpath='{.data.replicas-before}' 2>/dev/null || echo "")"
kubectl -n "$APP_NAMESPACE" scale deployment "$WORKLOAD" --replicas="${BEFORE:-1}" >/dev/null 2>&1 || true
kubectl -n "$APP_NAMESPACE" delete configmap "$STATE" --ignore-not-found >/dev/null 2>&1 || true

echo "Removed the upgrade PDB and scaled ${WORKLOAD} back to ${BEFORE:-1} replica(s)."
