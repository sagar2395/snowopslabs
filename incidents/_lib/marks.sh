#!/usr/bin/env bash
# Shared helpers for the labfault-* bookkeeping annotations.
#
# Sourced, not executed:
#   . "$PROJECT_ROOT/incidents/_lib/marks.sh"

# clear_marks <namespace> <deployment> <annotation>... — remove bookkeeping
# annotations from a Deployment AND from its ReplicaSets.
#
# The ReplicaSets matter. `kubectl rollout undo` restores the target
# ReplicaSet's annotations onto the Deployment, so annotations removed from the
# Deployment alone come back the moment a learner rolls a release back — which
# is a fix the hints actively recommend. The resurrected marks then convince
# inject.sh the fault is already present, and it refuses to run.
clear_marks() {
  ns="$1"
  deploy="$2"
  shift 2

  args=""
  for key in "$@"; do
    args="$args ${key}-"
  done

  # shellcheck disable=SC2086 # deliberate word splitting: one flag per key
  kubectl -n "$ns" annotate deploy "$deploy" $args --overwrite >/dev/null 2>&1 || true

  for rs in $(kubectl -n "$ns" get rs -o name 2>/dev/null | head -20); do
    owner="$(kubectl -n "$ns" get "$rs" -o 'jsonpath={.metadata.ownerReferences[0].name}' 2>/dev/null || true)"
    [ "$owner" = "$deploy" ] || continue
    # shellcheck disable=SC2086 # deliberate word splitting: one flag per key
    kubectl -n "$ns" annotate "$rs" $args --overwrite >/dev/null 2>&1 || true
  done
}
