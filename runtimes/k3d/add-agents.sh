#!/usr/bin/env bash
set -euo pipefail

# Grow a k3d cluster to TOTAL agent nodes, each ready to run the lab's pods.
# Usage: add-agents.sh <total-agents>
#
# Config (env): CLUSTER_NAME (default snowops).

TOTAL="${1:?usage: add-agents.sh <total-agents>}"
CLUSTER_NAME="${CLUSTER_NAME:-snowops}"

agent_names() {
  kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name 2>/dev/null | sed 's|^node/||'
}

# shellcheck source=runtimes/_lib/lab-images.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/_lib/lab-images.sh"
# shellcheck source=runtimes/_lib/docker.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/_lib/docker.sh"

current="$(agent_names | grep -c . || true)"
if [ "$current" -ge "$TOTAL" ]; then
  echo "Cluster '$CLUSTER_NAME' already has $current agent node(s)."
  exit 0
fi

# Each new agent offers the scheduler one share of Docker's memory, split over
# the server and TOTAL agents, rather than all of it.
node_args=()
reserved="$(node_memory_reserved $((TOTAL + 1)))"
if [ -n "$reserved" ]; then
  node_args+=(--k3s-arg "--kubelet-arg=system-reserved=memory=${reserved}Mi")
fi

i=0
added=""
while [ "$current" -lt "$TOTAL" ]; do
  name="${CLUSTER_NAME}-agent-${i}"
  i=$((i + 1))
  # `k3d node create NAME` names the node k3d-NAME-0; init's agents are
  # k3d-NAME. Skip a name either form already uses.
  node="k3d-${name}-0"
  if agent_names | grep -qx -e "k3d-${name}" -e "$node"; then
    continue
  fi
  echo "Adding agent node ${node}..."
  # The +-expansion keeps bash 3.2 (macOS) quiet about an empty array under set -u.
  k3d node create "$name" --cluster "$CLUSTER_NAME" --role agent ${node_args[@]+"${node_args[@]}"} --wait --timeout 300s
  docker exec "$node" sysctl -w fs.inotify.max_user_instances=512 >/dev/null 2>&1 || true
  added="$added $node"
  current=$((current + 1))
done

images="$(lab_images .)"
if [ -n "$images" ]; then
  echo "Importing the lab's app images into the new node(s)..."
  # shellcheck disable=SC2086 # one image per word
  k3d image import $images --cluster "$CLUSTER_NAME"
fi

# shellcheck disable=SC2086 # one node per word
kubectl wait --for=condition=Ready node $added --timeout=180s
echo "Cluster '$CLUSTER_NAME' now has $current agent node(s)."
