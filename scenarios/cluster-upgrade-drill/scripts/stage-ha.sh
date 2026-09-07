#!/usr/bin/env bash
set -euo pipefail

# stage-ha.sh — put go-api into the shape the drill needs: enough replicas that
# a PodDisruptionBudget has something to protect. Staging only; the learner
# still writes the PDB and performs the roll.

APP_NAMESPACE="${APP_NAMESPACE:-go-api}"
REPLICAS="${UPGRADE_DRILL_REPLICAS:-3}"

echo "==> Scaling go-api to ${REPLICAS} replicas for the upgrade drill"
kubectl -n "$APP_NAMESPACE" scale deployment go-api --replicas="$REPLICAS"

# Only wait for the HA floor the drill actually needs. Insisting on all
# replicas would fail on a lab with fewer schedulable nodes than replicas.
echo "==> Waiting for at least 2 ready replicas"
for _ in $(seq 1 60); do
  ready="$(kubectl -n "$APP_NAMESPACE" get deployment go-api \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)"
  if [ "${ready:-0}" -ge 2 ]; then
    echo "go-api has ${ready} ready replicas — staged."
    exit 0
  fi
  sleep 5
done

echo "ERROR: go-api did not reach 2 ready replicas in 300s." >&2
kubectl -n "$APP_NAMESPACE" get pods -o wide >&2
exit 1
