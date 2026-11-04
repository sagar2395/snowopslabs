#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

# inflate.sh - put the workload into the deliberately over-provisioned 'before' state
# (up to 40x CPU / 32x memory, as much as the lab's nodes can schedule) so the
# waste is visible in OpenCost before you right-size.
#
# We mutate the running Deployment in place with `kubectl set resources` - the
# exact inverse of the fix the learner performs (`kubectl set resources ...
# --requests=cpu=50m,memory=32Mi`). This is deliberately symmetric and robust:
# it works regardless of how the workload was deployed (helm release, `kubectl apply`,
# or the kind-e2e bootstrap) and regardless of which field-manager owns the
# resource fields. An earlier `helm upgrade` approach failed with server-side
# apply conflicts ("conflicts with kubectl") whenever the workload was not owned by
# helm - `kubectl set resources` sidesteps that entirely.

NAMESPACE="${WORKLOAD_NAMESPACE}"
APP="${WORKLOAD_NAME}"

# The inflated 'before' values. Requests drive scheduling and node billing;
# limits are matched to requests here so the pod is a single fat, wasteful slot.
# Prefixed, because CPU_REQUEST / MEMORY_REQUEST are the names an app's own
# app.env uses for its contractual sizing. Sharing them would let a sourced
# app.env silently make this script a no-op - the scenario would activate
# already right-sized and every check would be green before the learner starts.
# Requests left unset are sized from the room on the nodes below.
INFLATED_CPU_REQUEST="${INFLATED_CPU_REQUEST:-}"
INFLATED_MEM_REQUEST="${INFLATED_MEM_REQUEST:-}"

# The most the drill reserves, and the least that still shows the waste: five
# and two times the ceilings the check scripts grade against (100m, 256Mi).
MAX_CPU_M=2000
MAX_MEM_MI=1024
MIN_CPU_M=500
MIN_MEM_MI=512

if ! kubectl get deployment "$APP" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "ERROR: deployment ${NAMESPACE}/${APP} not found." >&2
  echo "Deploy it first: labctl app deploy ${APP}" >&2
  exit 1
fi

# Prints "<cpu millicores> <memory MiB>" that no running pod requests on the
# schedulable node with the most free CPU. The workload's own pod counts: the
# rolling update keeps it until the inflated pod is ready. Terminating pods do
# not, since their room comes back while the rollout waits.
free_on_roomiest_node() {
  {
    kubectl get nodes -o 'jsonpath={range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.cpu}{"\t"}{.status.allocatable.memory}{"\t"}{.spec.unschedulable}{"\t"}{.spec.taints[*].effect}{"\n"}{end}' |
      awk '{ print "N\t" $0 }'
    kubectl get pods -A --field-selector=status.phase!=Succeeded,status.phase!=Failed \
      -o 'jsonpath={range .items[*]}{.spec.nodeName}{"\t"}{.metadata.deletionTimestamp}{"\t"}{range .spec.containers[*]}{.resources.requests.cpu}{","}{end}{"\t"}{range .spec.containers[*]}{.resources.requests.memory}{","}{end}{"\n"}{end}' |
      awk '{ print "P\t" $0 }'
  } | awk -F'\t' '
    function cpu_m(q) {
      if (q ~ /m$/) return substr(q, 1, length(q) - 1) + 0
      return q * 1000
    }
    function mem_mi(q,   n) {
      n = q + 0
      if (q ~ /Ki$/) return n / 1024
      if (q ~ /Mi$/) return n
      if (q ~ /Gi$/) return n * 1024
      if (q ~ /Ti$/) return n * 1048576
      if (q ~ /k$/) return n * 1000 / 1048576
      if (q ~ /M$/) return n * 1000000 / 1048576
      if (q ~ /G$/) return n * 1000000000 / 1048576
      if (q ~ /T$/) return n * 1000000000000 / 1048576
      return n / 1048576
    }
    function total(list, kind,   parts, i, n, t) {
      n = split(list, parts, ",")
      t = 0
      for (i = 1; i <= n; i++) {
        if (parts[i] == "") continue
        t += (kind == "cpu") ? cpu_m(parts[i]) : mem_mi(parts[i])
      }
      return t
    }
    $1 == "N" && $2 != "" && $5 != "true" && $6 !~ /NoSchedule|NoExecute/ {
      cpu[$2] = cpu_m($3)
      mem[$2] = mem_mi($4)
    }
    $1 == "P" && ($2 in cpu) && $3 == "" {
      cpu[$2] -= total($4, "cpu")
      mem[$2] -= total($5, "mem")
    }
    END {
      best = ""
      for (n in cpu) if (best == "" || cpu[n] > cpu[best]) best = n
      if (best != "") printf "%d %d\n", cpu[best], mem[best]
    }'
}

if [ -z "$INFLATED_CPU_REQUEST" ] || [ -z "$INFLATED_MEM_REQUEST" ]; then
  # A rollout still in progress (a teardown just before) keeps the previous
  # pod and its requests until it ends, so measure once it has.
  kubectl -n "$NAMESPACE" rollout status deployment "$APP" --timeout=2m >/dev/null || true
  free="$(free_on_roomiest_node)"
  if [ -z "$free" ]; then
    echo "ERROR: found no schedulable node to size the over-provisioned ${APP} against." >&2
    echo "Check the lab's nodes: kubectl get nodes" >&2
    exit 1
  fi
  free_cpu_m="${free% *}"
  free_mem_mi="${free#* }"

  # Three quarters of the free room, so the pod still schedules if something
  # else starts meanwhile.
  cpu_m=$((free_cpu_m * 3 / 4 / 100 * 100))
  [ "$cpu_m" -le "$MAX_CPU_M" ] || cpu_m=$MAX_CPU_M
  mem_mi=$((free_mem_mi * 3 / 4 / 64 * 64))
  [ "$mem_mi" -le "$MAX_MEM_MI" ] || mem_mi=$MAX_MEM_MI

  if [ "$cpu_m" -lt "$MIN_CPU_M" ] || [ "$mem_mi" -lt "$MIN_MEM_MI" ]; then
    echo "ERROR: no node has room for an over-provisioned ${APP}." >&2
    echo "The roomiest node has ${free_cpu_m}m CPU and ${free_mem_mi}Mi memory that no pod requests;" >&2
    echo "the drill needs $(((MIN_CPU_M * 4 + 2) / 3))m CPU and $(((MIN_MEM_MI * 4 + 2) / 3))Mi memory free on one node." >&2
    echo "Stop another scenario first (labctl scenario down <name>), or give Docker more" >&2
    echo "CPUs and memory: docs/resources.md" >&2
    exit 1
  fi

  INFLATED_CPU_REQUEST="${INFLATED_CPU_REQUEST:-${cpu_m}m}"
  INFLATED_MEM_REQUEST="${INFLATED_MEM_REQUEST:-${mem_mi}Mi}"
fi
INFLATED_CPU_LIMIT="${INFLATED_CPU_LIMIT:-$INFLATED_CPU_REQUEST}"
INFLATED_MEM_LIMIT="${INFLATED_MEM_LIMIT:-$INFLATED_MEM_REQUEST}"

# Record the sizing the workload arrived with, so `scenario down` can put it
# back. Without this the scenario permanently mutates a workload every other
# scenario shares, and teardown silently leaves it over-provisioned.
MARK="snowops.net/cost-right-sizing-original"
ORIG_CPU_REQ="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.cpu}' 2>/dev/null || true)"
ORIG_MEM_REQ="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || true)"
ORIG_CPU_LIM="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.cpu}' 2>/dev/null || true)"
ORIG_MEM_LIM="$(kubectl -n "$NAMESPACE" get deploy "$APP" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true)"
kubectl -n "$NAMESPACE" annotate deploy "$APP" \
  "$MARK-cpu-request=${ORIG_CPU_REQ:-none}" "$MARK-mem-request=${ORIG_MEM_REQ:-none}" \
  "$MARK-cpu-limit=${ORIG_CPU_LIM:-none}" "$MARK-mem-limit=${ORIG_MEM_LIM:-none}" \
  --overwrite >/dev/null

echo "Over-provisioning ${NAMESPACE}/${APP}: requests cpu=${INFLATED_CPU_REQUEST}, memory=${INFLATED_MEM_REQUEST} (limits ${INFLATED_CPU_LIMIT}/${INFLATED_MEM_LIMIT})..."
kubectl -n "$NAMESPACE" set resources deployment "$APP" \
  --requests="cpu=${INFLATED_CPU_REQUEST},memory=${INFLATED_MEM_REQUEST}" \
  --limits="cpu=${INFLATED_CPU_LIMIT},memory=${INFLATED_MEM_LIMIT}"

echo "Waiting for the over-provisioned pod to roll out..."
kubectl -n "$NAMESPACE" rollout status deployment "$APP" --timeout=5m

echo "Done. ${APP} now requests ${INFLATED_CPU_REQUEST} CPU / ${INFLATED_MEM_REQUEST} memory (the inflated baseline)."
echo "Observe the cost in OpenCost, then right-size with 'kubectl -n ${NAMESPACE} set resources'."
