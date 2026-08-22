#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="chaos-mesh"
SCRIPT_DIR="$(dirname "$0")"

# Select the containerd socket path based on the active runtime profile.
# Each container runtime / cloud provider places the socket at a different path:
#   k3d  → /run/k3s/containerd/containerd.sock  (k3s embeds its own containerd)
#   aks  → /run/containerd/containerd.sock       (standard containerd on Azure nodes)
#   eks  → /run/containerd/containerd.sock       (standard containerd on Bottlerocket/AL2 nodes)
case "${PROFILE:-k3d}" in
  k3d) CONTAINERD_SOCKET="/run/k3s/containerd/containerd.sock" ;;
  aks | eks) CONTAINERD_SOCKET="/run/containerd/containerd.sock" ;;
  *) CONTAINERD_SOCKET="/run/containerd/containerd.sock" ;;
esac

echo "Installing Chaos Mesh (profile=${PROFILE:-k3d}, containerd socket=${CONTAINERD_SOCKET})..."

# Create namespace if it doesn't exist
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

# Add Helm repo and update
echo "Adding Chaos Mesh Helm repository..."
helm repo add chaos-mesh https://charts.chaos-mesh.org >/dev/null 2>&1 || true
helm repo update

# Install or upgrade Chaos Mesh with the runtime-appropriate socket path.
# The socketPath is NOT in values.yaml (which would be k3d-only) — it is passed
# here via --set so cloud runtimes work without any values.yaml changes.
echo "Installing Chaos Mesh chart..."
helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh \
  --namespace $NAMESPACE \
  --create-namespace \
  --set "chaosDaemon.socketPath=${CONTAINERD_SOCKET}" \
  -f "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 3m

# Wait for controller manager to be ready
echo "Waiting for Chaos Mesh controller manager to be ready..."
kubectl rollout status deployment/chaos-controller-manager -n $NAMESPACE --timeout=180s || true

# Wait for dashboard
echo "Waiting for Chaos Mesh dashboard to be ready..."
kubectl rollout status deployment/chaos-dashboard -n $NAMESPACE --timeout=120s || true

echo ""
echo "Chaos Mesh installed successfully"
echo "Namespace: $NAMESPACE"
echo "Status: kubectl get pods -n $NAMESPACE"
echo ""
echo "Dashboard: kubectl port-forward -n $NAMESPACE svc/chaos-dashboard 2333:2333"
echo "  Then open http://localhost:2333"
echo ""
echo "Create experiments via CRDs or the Chaos Dashboard UI."
