#!/usr/bin/env bash
# Passes (exit 0) when the fault is resolved: the rollout completes AND the pods
# of the current ReplicaSet are not being OOMKilled.
#
# Rollout status alone passes in the window between the limit cut and the first
# kill, which used to grade the fault as already fixed. Scoping the OOM guard to
# the current ReplicaSet matters just as much: a pod from the broken rollout is
# still terminating when the fix lands, and counting its history marks a correct
# fix as wrong.
set -euo pipefail
NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"

# kubectl's own failure here is "error: timed out waiting for the condition",
# which tells a learner nothing about what it was waiting for.
if ! kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=15s >/dev/null 2>&1; then
  echo "FAIL: the rollout of $NS/$DEPLOY has not completed." >&2
  kubectl -n "$NS" get pods -l "app.kubernetes.io/name=$DEPLOY" --no-headers \
    -o 'custom-columns=POD:.metadata.name,READY:.status.containerStatuses[0].ready,LAST-TERMINATED:.status.containerStatuses[0].lastState.terminated.reason,RESTARTS:.status.containerStatuses[0].restartCount' \
    2>/dev/null | sed 's/^/  /' >&2 || true
  echo "  Stuck? 'labctl incident hint' walks you in." >&2
  exit 1
fi

# Select the ReplicaSet that is actually serving, not the newest one: a
# Deployment reuses an existing ReplicaSet whenever a pod template it has seen
# before comes back, so creation order does not track revision order. After a
# completed rollout exactly one ReplicaSet is scaled above zero.
HASH="$(kubectl -n "$NS" get rs \
  -o "jsonpath={range .items[?(@.spec.replicas>0)]}{.metadata.ownerReferences[0].name}{'='}{.metadata.labels.pod-template-hash}{'\n'}{end}" 2>/dev/null |
  grep "^$DEPLOY=" | head -1 | cut -d= -f2)"
[ -n "$HASH" ] || exit 0

reasons="$(kubectl -n "$NS" get pods -l "pod-template-hash=$HASH" \
  -o 'jsonpath={.items[*].status.containerStatuses[*].lastState.terminated.reason}' 2>/dev/null || true)"
case "$reasons" in
  *OOMKilled*)
    echo "FAIL: a pod in the current ReplicaSet was last terminated with OOMKilled." >&2
    echo "  Stuck? 'labctl incident hint' walks you in." >&2
    exit 1
    ;;
esac
