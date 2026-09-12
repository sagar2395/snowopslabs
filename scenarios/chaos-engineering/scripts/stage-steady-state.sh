#!/usr/bin/env bash
# Put the workload into the state this drill's premise depends on, and clear
# anything a previous session left behind.
#
# The scenario's whole first act is "the PDB reports disruptionsAllowed 0
# because there is only one replica". Nothing guaranteed that: another scenario
# (node-drain-drill, cluster-upgrade-drill) scales the same shared workload out,
# and if it left it at three the drill is already finished before the learner
# arrives — 'workload-survives-pod-loss' is green at activation and the lesson
# never happens.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

NS="${WORKLOAD_NAMESPACE}"
WORKLOAD="${WORKLOAD_NAME}"
STATE="chaos-steady-state"

# Chaos objects are applied by the learner from the explore commands, so they
# are not components and 'scenario down' has never removed them. One left over
# from a previous session makes 'an experiment was run' true before anyone runs
# one.
STALE="$(kubectl -n "$NS" get podchaos,networkchaos,stresschaos -o name 2>/dev/null | grep -c . || true)"
if [ "${STALE:-0}" -gt 0 ]; then
  echo "Clearing ${STALE} chaos experiment(s) left in ${NS} by an earlier run."
  kubectl -n "$NS" delete podchaos,networkchaos,stresschaos --all --ignore-not-found >/dev/null 2>&1 || true
fi
CURRENT="$(kubectl -n "$NS" get deploy "$WORKLOAD" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")"
if [ -z "$CURRENT" ]; then
  echo "WARNING: ${NS}/${WORKLOAD} is not deployed; the drill has nothing to experiment on." >&2
  exit 0
fi

# Remember what we found so teardown can hand the workload back unchanged — but
# only once. A re-activation with --force runs over a cluster this scenario has
# already staged, and re-recording would capture the drill's own doing.
if kubectl -n "$NS" get configmap "$STATE" >/dev/null 2>&1; then
  echo "Keeping the replica count recorded at the first activation."
else
  kubectl -n "$NS" create configmap "$STATE" \
    --from-literal=replicas-before="$CURRENT" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
fi

if [ "$CURRENT" -gt 1 ]; then
  echo "${WORKLOAD} is running ${CURRENT} replicas; scaling to 1 so the PDB starts out unable to allow a disruption."
  kubectl -n "$NS" scale deploy "$WORKLOAD" --replicas=1 >/dev/null
  kubectl -n "$NS" rollout status deploy "$WORKLOAD" --timeout=120s >/dev/null 2>&1 || true
fi

# A second PDB selecting the same pods changes what a drain does, and this
# scenario's lesson is about exactly one budget. Warn rather than fail: the
# other scenario may be legitimately active.
OTHER="$(kubectl -n "$NS" get pdb --no-headers \
  -o 'custom-columns=NAME:.metadata.name,APP:.spec.selector.matchLabels.app' 2>/dev/null |
  awk -v app="$WORKLOAD" -v mine="${WORKLOAD}-pdb" '$2 == app && $1 != mine { print $1 }' || true)"
if [ -n "$OTHER" ]; then
  echo "WARNING: another PodDisruptionBudget already selects ${WORKLOAD}:"
  printf '  %s\n' $OTHER
  echo "  Two budgets over one workload make 'disruptions allowed' the MINIMUM of both,"
  echo "  so this drill's numbers will not be the ones it describes. That budget belongs"
  echo "  to another scenario — tear that one down first."
fi

echo "Steady state staged: ${WORKLOAD} at 1 replica in ${NS}."
