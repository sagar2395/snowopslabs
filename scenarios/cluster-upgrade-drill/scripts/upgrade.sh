#!/usr/bin/env bash
set -euo pipefail

# upgrade.sh — roll the k3d cluster's worker (agent) nodes to a newer k3s
# version one at a time, draining each before it is replaced so go-api stays
# available throughout.
#
# HONEST SCOPE: k3d has no in-place node upgrade. A faithful simulation of a
# rolling worker upgrade is "drain -> delete node -> add a node on the new
# image". The server/control-plane node is intentionally left untouched; on a
# managed cluster the provider upgrades the control plane first. See the
# scenario tips and docs/runbooks/11-multi-env-day2.md.
#
# Env:
#   CLUSTER_NAME        k3d cluster name (default: snowops)
#   TARGET_K3S_VERSION  k3s image tag to upgrade agents to (REQUIRED,
#                       e.g. v1.29.4-k3s1)
#   APP_NAMESPACE       go-api namespace (default: go-api)
#   DRAIN_TIMEOUT       per-node drain timeout (default: 120s)

CLUSTER_NAME="${CLUSTER_NAME:-snowops}"
APP_NAMESPACE="${APP_NAMESPACE:-go-api}"
DRAIN_TIMEOUT="${DRAIN_TIMEOUT:-120s}"

if [ -z "${TARGET_K3S_VERSION:-}" ]; then
  echo "ERROR: TARGET_K3S_VERSION is required (e.g. v1.29.4-k3s1)." >&2
  echo "       Check the current version first: kubectl get nodes" >&2
  exit 1
fi

for bin in k3d kubectl; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "ERROR: '$bin' is required but not installed." >&2
    exit 1
  fi
done

if ! k3d cluster list "$CLUSTER_NAME" >/dev/null 2>&1; then
  echo "ERROR: k3d cluster '${CLUSTER_NAME}' not found. Run 'labctl runtime up' first." >&2
  exit 1
fi

# Ensure go-api has enough replicas for the PDB to protect availability.
echo "==> Ensuring go-api has HA replicas (>=3) before the upgrade"
kubectl -n "$APP_NAMESPACE" scale deployment go-api --replicas=3
kubectl -n "$APP_NAMESPACE" rollout status deployment go-api --timeout=120s

# List agent nodes managed by k3d for this cluster. k3d node names match the
# Kubernetes node names, so we can drain by the same identifier. Use a portable
# while-read loop (not mapfile) so this works on macOS's default bash 3.2.
AGENTS=()
while IFS= read -r _node; do
  [ -n "$_node" ] && AGENTS+=("$_node")
done < <(k3d node list -o json 2>/dev/null |
  grep -o "\"name\":\"k3d-${CLUSTER_NAME}-agent-[0-9]*\"" |
  sed 's/.*"name":"//;s/"$//' |
  sort -u || true)

if [ "${#AGENTS[@]}" -eq 0 ]; then
  echo "ERROR: no agent nodes found for cluster '${CLUSTER_NAME}'." >&2
  echo "       Recreate with agents: AGENTS=2 labctl runtime up" >&2
  exit 1
fi

echo "==> Rolling ${#AGENTS[@]} agent node(s) to ${TARGET_K3S_VERSION}, one at a time"

roll_node() {
  local node="$1"
  echo ""
  echo "--- Upgrading ${node} ------------------------------------------"

  echo "==> Cordoning and draining ${node}"
  kubectl cordon "$node"
  if ! kubectl drain "$node" \
    --ignore-daemonsets \
    --delete-emptydir-data \
    --timeout="$DRAIN_TIMEOUT"; then
    echo "WARN: drain of ${node} did not fully complete; uncordoning and aborting." >&2
    kubectl uncordon "$node" || true
    return 1
  fi

  echo "==> Removing old node ${node} from the cluster"
  k3d node delete "$node"

  # Recreate an agent on the target image. k3d auto-names new nodes; the
  # replacement joins the same cluster and the kube-scheduler places go-api
  # pods back onto it.
  local newname="${node}-up"
  echo "==> Adding replacement agent on rancher/k3s:${TARGET_K3S_VERSION}"
  k3d node create "$newname" \
    --cluster "$CLUSTER_NAME" \
    --role agent \
    --image "rancher/k3s:${TARGET_K3S_VERSION}" \
    --wait

  echo "==> Waiting for go-api to settle before the next node"
  kubectl -n "$APP_NAMESPACE" rollout status deployment go-api --timeout=120s
}

for node in "${AGENTS[@]}"; do
  roll_node "$node"
done

echo ""
echo "Rolling worker upgrade complete."
echo "Confirm versions:  kubectl get nodes -o wide"
echo "Grade availability: labctl scenario verify cluster-upgrade-drill"
