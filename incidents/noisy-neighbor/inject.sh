#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="${PROJECT_ROOT:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
# shellcheck source=incidents/_lib/render.sh
. "$PROJECT_ROOT/incidents/_lib/render.sh"

VICTIM_NS="${WORKLOAD_NAMESPACE:-go-api}"
VICTIM="${WORKLOAD_NAME:-go-api}"
MON_NS="${MONITORING_NAMESPACE:-monitoring}"

# Nothing here names the fault. The learner's brief is fault.yaml's description
# and the page this arms; a progress line reading "deploying a CPU burner" hands
# over the answer before the drill starts.
echo "Scheduling a batch workload..."

# Which node is the victim on? Everything below follows from that: contention is
# a property of a shared node, so the burner is placed there deliberately.
NODE="$(kubectl -n "$VICTIM_NS" get pods --no-headers \
  -o custom-columns=NAME:.metadata.name,NODE:.spec.nodeName,PHASE:.status.phase 2>/dev/null |
  awk -v d="$VICTIM" '$3 == "Running" && index($1, d) == 1 { print $2; exit }')"

if [ -z "${NODE:-}" ]; then
  # Distinguish "the app is not deployed" from "there is no cluster here". The
  # first is a real precondition the learner has to fix; the second is the
  # contract test, which runs this script against a stub kubectl that answers
  # every read with nothing. Pinning is meaningless without a cluster, so the
  # dry run continues unpinned rather than reporting a failure it invented.
  if [ -n "$(kubectl get nodes -o name 2>/dev/null || true)" ]; then
    echo "ERROR: no Running ${VICTIM} pod in namespace ${VICTIM_NS}, so there is no" >&2
    echo "  neighbour to be noisy to. Deploy the app first:  labctl app deploy ${VICTIM}" >&2
    exit 1
  fi
  echo "No cluster reachable — staging the manifests without node placement."
fi

# Size the burner to the node it is landing on. A fixed replica count is either
# a no-op on a large node or a stampede on a small one, and the symptom the
# fault promises — a saturated node — has to actually happen.
CORES="$(kubectl get node "${NODE:-}" -o jsonpath='{.status.allocatable.cpu}' 2>/dev/null |
  awk '{ if ($0 ~ /m$/) { sub(/m$/, ""); printf "%d", $0 / 1000 } else { printf "%d", $0 } }')"
[ "${CORES:-0}" -ge 2 ] || CORES=2
REPLICAS="$CORES"
[ "$REPLICAS" -le 6 ] || REPLICAS=6

# resolve.sh deletes the namespace without waiting, so an inject that follows a
# resolve closely finds it still Terminating. kubectl apply into a Terminating
# namespace reports "unchanged" and exits 0 — the fault would be recorded as
# injected while the deployment it just "created" is being garbage-collected,
# and the learner is handed an incident that is not happening.
NS_PHASE="$(kubectl get ns "${TARGET_NAMESPACE:-labfault-batch}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
if [ "${NS_PHASE:-}" = "Terminating" ]; then
  echo "Waiting for the previous run's namespace to finish deleting..."
  for _ in $(seq 1 60); do
    kubectl get ns "${TARGET_NAMESPACE:-labfault-batch}" >/dev/null 2>&1 || break
    sleep 2
  done
  if kubectl get ns "${TARGET_NAMESPACE:-labfault-batch}" >/dev/null 2>&1; then
    echo "ERROR: namespace ${TARGET_NAMESPACE:-labfault-batch} is still Terminating after 120s." >&2
    echo "  Injecting now would silently produce an empty fault. Wait for it to clear." >&2
    exit 1
  fi
fi

render_targeted "$SCRIPT_DIR/manifests/burner.yaml" |
  sed -e "s|\${BURNER_NODE}|${NODE}|g" -e "s|\${BURNER_REPLICAS}|${REPLICAS}|g" |
  kubectl apply -f -

# Arm the page. The alert watches node CPU, not this namespace: on-call is told
# a node is saturated and has to go and find out by whom, which is the drill.
render_targeted "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - >/dev/null 2>&1 ||
  echo "Note: the alert rule could not be applied — is kube-prometheus-stack installed?" >&2

echo "Done. It may take a few minutes for the effect to show up in your metrics."
echo "Tip: run 'labctl traffic start' so there is real traffic to degrade."
