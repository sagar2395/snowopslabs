#!/usr/bin/env bash
# Shared assertions for incident detection checks.
#
# Sourced, not executed:
#   . "$(cd "$(dirname "$0")/../../_lib" && pwd)/assert.sh"

# assert_workload_healthy <namespace> <deployment>
#
# Exits non-zero unless the workload is genuinely serving.
#
# `kubectl rollout status` alone is NOT enough: it reports success on a
# Deployment scaled to zero, so a learner who runs `kubectl scale --replicas=0`
# grades a still-broken lab as fixed. The fault's own spec is untouched; there is
# simply nothing left to be unhealthy. Verified: scaling go-api to 0 made the
# bare rollout-status check exit 0 while the bad image was still in the spec.
#
# So check the replica count first, then that the rollout actually completed.
assert_workload_healthy() {
  ns="$1"
  deploy="$2"

  desired="$(kubectl -n "$ns" get deploy "$deploy" -o 'jsonpath={.spec.replicas}' 2>/dev/null || true)"
  if [ -z "$desired" ]; then
    echo "FAIL: deployment $ns/$deploy not found." >&2
    return 1
  fi
  if [ "$desired" -lt 1 ]; then
    echo "FAIL: $ns/$deploy is scaled to $desired replicas, so nothing is serving." >&2
    echo "  Scaling to zero silences the symptom without fixing the fault." >&2
    echo "  Restore it: kubectl -n $ns scale deploy $deploy --replicas=1" >&2
    return 1
  fi

  # kubectl's own failure here is "error: timed out waiting for the condition",
  # which tells a learner nothing about what it was waiting for. Say what the
  # pods are actually doing instead.
  if ! kubectl -n "$ns" rollout status "deploy/$deploy" --timeout=15s >/dev/null 2>&1; then
    echo "FAIL: the rollout of $ns/$deploy has not completed." >&2
    kubectl -n "$ns" get pods -l "app.kubernetes.io/name=$deploy" --no-headers \
      -o 'custom-columns=POD:.metadata.name,READY:.status.containerStatuses[0].ready,REASON:.status.containerStatuses[0].state.waiting.reason,RESTARTS:.status.containerStatuses[0].restartCount' \
      2>/dev/null | sed 's/^/  /' >&2 || true
    echo "  Stuck? 'labctl incident hint' walks you in." >&2
    return 1
  fi

  ready="$(kubectl -n "$ns" get deploy "$deploy" -o 'jsonpath={.status.readyReplicas}' 2>/dev/null || true)"
  if [ "${ready:-0}" -lt "$desired" ]; then
    echo "FAIL: $ns/$deploy has ${ready:-0}/$desired replicas ready." >&2
    return 1
  fi
  return 0
}
