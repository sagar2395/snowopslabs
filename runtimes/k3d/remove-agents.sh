#!/usr/bin/env bash
set -euo pipefail

# Shrink a k3d cluster towards TOTAL agent nodes by removing the agents
# add-agents.sh created. The agents `labctl init` created stay, and so does a
# node that holds a local volume, because its data is deleted with the node.
# Usage: remove-agents.sh <total-agents>
#
# Config (env): CLUSTER_NAME (default snowops).

# shellcheck source=runtimes/_lib/k3d-serverlb.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/_lib/k3d-serverlb.sh"

TOTAL="${1:?usage: remove-agents.sh <total-agents>}"
CLUSTER_NAME="${CLUSTER_NAME:-snowops}"

agent_names() {
  kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name 2>/dev/null | sed 's|^node/||'
}

# added_agents lists the agents add-agents.sh created (k3d-NAME-agent-N-0),
# newest first.
added_agents() {
  agent_names | grep -E "^k3d-${CLUSTER_NAME}-agent-[0-9]+-0$" | sort -r || true
}

# volume_nodes lists the nodes that local persistent volumes are pinned to.
volume_nodes() {
  kubectl get pv -o jsonpath='{range .items[*]}{.spec.nodeAffinity.required.nodeSelectorTerms[*].matchExpressions[*].values[*]}{"\n"}{end}' 2>/dev/null || true
}

current="$(agent_names | grep -c . || true)"
if [ "$current" -le "$TOTAL" ]; then
  exit 0
fi

added="$(added_agents)"
[ -n "$added" ] || exit 0

pinned="$(volume_nodes)"
for node in $added; do
  [ "$current" -gt "$TOTAL" ] || break
  if printf '%s\n' "$pinned" | grep -qx "$node"; then
    echo "Keeping agent node $node: it holds a local volume that would be lost with it."
    continue
  fi
  echo "Removing agent node $node..."
  if ! kubectl drain "$node" --ignore-daemonsets --delete-emptydir-data --timeout=120s; then
    kubectl uncordon "$node" >/dev/null 2>&1 || true
    echo "Keeping agent node $node: its pods could not be moved to another node."
    continue
  fi
  k3d node delete "$node"
  kubectl delete node "$node" --ignore-not-found >/dev/null
  current=$((current - 1))
  removed=1
done
# A rolled init agent (k3d-NAME-agent-0-0) looks like an added one and can be
# removed here. k3d keeps an agent's name in the load balancer after deleting it.
if [ -n "${removed:-}" ] && ! serverlb_repair "$CLUSTER_NAME"; then
  echo "WARN: could not check the load balancer's config; if the lab becomes" >&2
  echo "      unreachable after a restart, run 'labctl init'." >&2
fi
echo "Cluster '$CLUSTER_NAME' has $current agent node(s)."
