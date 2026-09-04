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
NS="${TARGET_NAMESPACE:-echo-server}"
DEPLOY="${TARGET_WORKLOAD:-echo-server}"

kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=15s

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
    echo "a pod in the current ReplicaSet was last terminated with OOMKilled" >&2
    exit 1
    ;;
esac
