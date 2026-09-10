#!/usr/bin/env bash
# Hand the shared workload back exactly as the scenario found it, and take the
# learner's experiments with it.
#
# Leaving chaos objects behind is not cosmetic: they are one-shot but they keep
# existing, and the next activation would see them and believe an experiment had
# already been run.
set -euo pipefail

NS="${WORKLOAD_NAMESPACE:-go-api}"
WORKLOAD="${WORKLOAD_NAME:-go-api}"
STATE="chaos-steady-state"

kubectl -n "$NS" delete podchaos,networkchaos,stresschaos --all --ignore-not-found >/dev/null 2>&1 || true
echo "Removed every chaos experiment in ${NS}."

BEFORE="$(kubectl -n "$NS" get configmap "$STATE" -o jsonpath='{.data.replicas-before}' 2>/dev/null || echo "")"
if [ -n "$BEFORE" ] && kubectl -n "$NS" get deploy "$WORKLOAD" >/dev/null 2>&1; then
  NOW="$(kubectl -n "$NS" get deploy "$WORKLOAD" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")"
  if [ "$NOW" != "$BEFORE" ]; then
    echo "Restoring ${WORKLOAD} to the ${BEFORE} replica(s) it had before the drill."
    kubectl -n "$NS" scale deploy "$WORKLOAD" --replicas="$BEFORE" >/dev/null 2>&1 || true
  fi
fi

kubectl -n "$NS" delete configmap "$STATE" chaos-experiment-witness --ignore-not-found >/dev/null 2>&1 || true
