#!/usr/bin/env bash
set -euo pipefail

# unstage-ha.sh — undo stage-ha.sh and remove the PodDisruptionBudget the
# learner applied, so 'scenario down' leaves nothing behind. ${WORKLOAD_NAME:-go-api} itself is an
# app prerequisite and stays running.

APP_NAMESPACE="${APP_NAMESPACE:-${WORKLOAD_NAMESPACE:-go-api}}"

kubectl -n "$APP_NAMESPACE" delete pdb ${WORKLOAD_NAME:-go-api}-upgrade-pdb --ignore-not-found
kubectl -n "$APP_NAMESPACE" scale deployment ${WORKLOAD_NAME:-go-api} --replicas=1 >/dev/null 2>&1 || true
echo "Removed the upgrade PDB and scaled ${WORKLOAD_NAME:-go-api} back to 1 replica."
