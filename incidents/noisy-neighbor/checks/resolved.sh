#!/usr/bin/env bash
# Passes (exit 0) when the fault is resolved: the batch workload can no longer
# monopolise the node's CPU.
#
# It deliberately does NOT require the deployment to be deleted. The hints and
# the solution both offer capping it — a CPU limit, a LimitRange, a quota — as
# the grown-up answer, and an existence check fails a learner who took exactly
# that advice. Grade the contention, not the object.
set -euo pipefail

NS="${TARGET_NAMESPACE:-labfault-batch}"
DEPLOY="${TARGET_WORKLOAD:-report-batch}"

if ! kubectl get deployment "$DEPLOY" -n "$NS" >/dev/null 2>&1; then
  echo "OK: the batch workload is gone."
  exit 0
fi

REPLICAS="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.replicas}' 2>/dev/null || echo 0)"
if [ "${REPLICAS:-0}" -eq 0 ]; then
  echo "OK: the batch workload is scaled to zero, so it is consuming nothing."
  exit 0
fi

# millicores <quantity> — "500m", "2", "1500m" all become an integer of millicores.
millicores() {
  awk -v q="$1" 'BEGIN {
    if (q == "") { print 0 }
    else if (q ~ /m$/) { sub(/m$/, "", q); printf "%d", q }
    else { printf "%d", q * 1000 }
  }'
}

# Grade the CURRENT ReplicaSet's pods, and nothing else.
#
# Reading every pod in the namespace looks equivalent and is not: for a minute
# after a rollout the old pods are still there, still in phase Running, still
# carrying the old resources. Judged against those, a learner who has just
# applied the correct fix is told they have not fixed it — and the next thing
# they do is distrust the grader.
#
# The pods have to be read rather than the Deployment's template, because a
# LimitRange supplies its default at admission: the template stays empty while
# every pod it produces is capped, and that is a legitimate fix.
HASH="$(kubectl -n "$NS" get rs -l app="$DEPLOY" \
  -o 'jsonpath={range .items[*]}{.metadata.creationTimestamp}{" "}{.spec.replicas}{" "}{.metadata.labels.pod-template-hash}{"\n"}{end}' \
  2>/dev/null | awk '$2 > 0 { print $1, $3 }' | sort | tail -1 | awk '{ print $2 }')"

SELECTOR="app=$DEPLOY"
[ -z "${HASH:-}" ] || SELECTOR="app=$DEPLOY,pod-template-hash=$HASH"

LIMITS="$(kubectl -n "$NS" get pods -l "$SELECTOR" --field-selector=status.phase=Running \
  -o 'jsonpath={range .items[*]}{.spec.containers[0].resources.limits.cpu}{"\n"}{end}' 2>/dev/null || true)"
CAPPED="$(printf '%s\n' "$LIMITS" | grep -c '.' || true)"

NODE="$(kubectl -n "$NS" get pods -l "$SELECTOR" --field-selector=status.phase=Running \
  -o 'jsonpath={.items[0].spec.nodeName}' 2>/dev/null || true)"

if [ "${CAPPED:-0}" -lt "${REPLICAS:-1}" ]; then
  echo "FAIL: only ${CAPPED} of ${REPLICAS} pod(s) in ${NS}/${DEPLOY} carry a CPU limit," >&2
  echo "  so nothing bounds what they take from ${NODE:-their node}." >&2
  echo "  Stuck? 'labctl incident hint' walks you in." >&2
  exit 1
fi

# A limit is only a fix if it is small enough to leave the node usable. Capping
# the workload at the size of the whole machine satisfies "has a limit" while
# the contention continues unchanged — the check would grade the letter of the
# fix and miss its point.
BIGGEST=0
for lim in $LIMITS; do
  m="$(millicores "$lim")"
  [ "$m" -le "$BIGGEST" ] || BIGGEST="$m"
done
TOTAL=$((BIGGEST * REPLICAS))

ALLOC="$(millicores "$(kubectl get node "$NODE" -o jsonpath='{.status.allocatable.cpu}' 2>/dev/null || echo 0)")"
BUDGET=$((ALLOC / 2))

if [ "$ALLOC" -gt 0 ] && [ "$TOTAL" -gt "$BUDGET" ]; then
  echo "FAIL: every pod in ${NS}/${DEPLOY} carries a CPU limit, but it is too big to be one:" >&2
  echo "  ${BIGGEST}m each, ${TOTAL}m across ${REPLICAS} replicas, on a node with ${ALLOC}m." >&2
  echo "  A limit only helps if it leaves room for the neighbours (aim for half the node)." >&2
  exit 1
fi

echo "OK: all ${REPLICAS} pod(s) are capped at ${BIGGEST}m, ${TOTAL}m in total on a ${ALLOC}m node —"
echo "    they can no longer starve their neighbours."
# Capping is the production answer, so the tenant is still on the node once the
# incident closes — and `incident resolve` with no argument refuses, because
# there is no longer an active incident to resolve. Say so here: this is the
# last thing the learner reads.
echo "    The tenant is still running, capped. Evict it with:"
echo "      labctl incident resolve noisy-neighbor"
exit 0
