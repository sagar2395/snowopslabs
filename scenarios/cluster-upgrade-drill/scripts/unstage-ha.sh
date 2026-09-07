#!/usr/bin/env bash
set -euo pipefail

# unstage-ha.sh — undo stage-ha.sh and remove the PodDisruptionBudget the
# learner applied, so 'scenario down' leaves nothing behind. go-api itself is an
# app prerequisite and stays running.

APP_NAMESPACE="${APP_NAMESPACE:-go-api}"

kubectl -n "$APP_NAMESPACE" delete pdb go-api-upgrade-pdb --ignore-not-found
kubectl -n "$APP_NAMESPACE" scale deployment go-api --replicas=1 >/dev/null 2>&1 || true
echo "Removed the upgrade PDB and scaled go-api back to 1 replica."
