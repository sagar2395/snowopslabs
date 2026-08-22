#!/usr/bin/env bash
set -euo pipefail

# Create a kind (Kubernetes IN Docker) cluster — a headless, CI-friendly local
# runtime (task 064). Mirrors k3d's host-port exposure: the control-plane node
# maps host 80/443 -> node 80/443 via extraPortMappings, and is labelled
# `ingress-ready=true` so an ingress controller can bind those ports. This keeps
# platform scripts runtime-agnostic.
#
# Config (env, with defaults — scripts never source .env themselves):
#   CLUSTER_NAME     cluster name (default: snowops)
#   HTTP_PORT        host port mapped to node :80 (default: 80)
#   HTTPS_PORT       host port mapped to node :443 (default: 443)
#   AGENTS           number of worker nodes (default: 1)
#   KIND_NODE_IMAGE  pin the node image, e.g. kindest/node:v1.29.4 (default: kind's)

CLUSTER_NAME="${1:-${CLUSTER_NAME:-snowops}}"
HTTP_PORT="${HTTP_PORT:-80}"
HTTPS_PORT="${HTTPS_PORT:-443}"
AGENTS="${AGENTS:-1}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-}"

for bin in docker kind kubectl; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "ERROR: '$bin' is required but not installed." >&2
    echo "       kind: https://kind.sigs.k8s.io/docs/user/quick-start/#installation" >&2
    exit 1
  fi
done

if ! docker info >/dev/null 2>&1; then
  echo "ERROR: Docker daemon is not reachable. Start Docker and retry." >&2
  exit 1
fi

# Skip if the cluster already exists (idempotent).
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "Cluster '$CLUSTER_NAME' already exists, skipping creation."
  kubectl config use-context "kind-$CLUSTER_NAME"
  exit 0
fi

# Build the kind config. The control-plane node carries the ingress-ready label
# and the host port mappings; worker nodes (AGENTS) give day-2 drills somewhere
# to reschedule pods.
node_image_line=""
if [ -n "$KIND_NODE_IMAGE" ]; then
  node_image_line="  image: ${KIND_NODE_IMAGE}"
fi

config_file="$(mktemp)"
trap 'rm -f "$config_file"' EXIT

{
  echo "kind: Cluster"
  echo "apiVersion: kind.x-k8s.io/v1alpha4"
  echo "nodes:"
  echo "  - role: control-plane"
  [ -n "$node_image_line" ] && echo "$node_image_line"
  echo "    kubeadmConfigPatches:"
  echo "      - |"
  echo "        kind: InitConfiguration"
  echo "        nodeRegistration:"
  echo "          kubeletExtraArgs:"
  echo '            node-labels: "ingress-ready=true"'
  echo "    extraPortMappings:"
  echo "      - containerPort: 80"
  echo "        hostPort: ${HTTP_PORT}"
  echo "        protocol: TCP"
  echo "      - containerPort: 443"
  echo "        hostPort: ${HTTPS_PORT}"
  echo "        protocol: TCP"
  i=0
  while [ "$i" -lt "$AGENTS" ]; do
    echo "  - role: worker"
    [ -n "$node_image_line" ] && echo "$node_image_line"
    i=$((i + 1))
  done
} >"$config_file"

echo "Creating kind cluster '$CLUSTER_NAME' (control-plane + ${AGENTS} worker(s))..."
kind create cluster --name "$CLUSTER_NAME" --config "$config_file" --wait 120s

kubectl config use-context "kind-$CLUSTER_NAME"

echo ""
echo "kind cluster '$CLUSTER_NAME' is ready."
echo "  Host ports ${HTTP_PORT}/${HTTPS_PORT} -> ingress on the control-plane node."
echo "  Install ingress: labctl platform up ingress"
