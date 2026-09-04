#!/usr/bin/env bash
set -euo pipefail

CHAOS_MESH_CHART_VERSION="${CHAOS_MESH_CHART_VERSION:-2.8.4}"

NAMESPACE="chaos-mesh"
SCRIPT_DIR="$(dirname "$0")"

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
echo "Installing Chaos Mesh chart..."
helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh \
  --version "$CHAOS_MESH_CHART_VERSION" \
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

# Expose the dashboard through Traefik at a stable URL.
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"
echo "Exposing Chaos Mesh dashboard at http://chaos.${DOMAIN_SUFFIX} ..."
kubectl apply -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: chaos-dashboard
  namespace: $NAMESPACE
spec:
  ingressClassName: traefik
  rules:
    - host: chaos.${DOMAIN_SUFFIX}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: chaos-dashboard
                port:
                  number: 2333
EOF

echo ""
echo "Chaos Mesh installed successfully"
echo "Namespace: $NAMESPACE"
echo "Status: kubectl get pods -n $NAMESPACE"
echo ""
echo "Dashboard: http://chaos.${DOMAIN_SUFFIX}  (run 'labctl hosts sync' once if the host doesn't resolve)"
echo "  Or port-forward: kubectl port-forward -n $NAMESPACE svc/chaos-dashboard 2333:2333  → http://localhost:2333"
echo ""
echo "Create experiments via CRDs or the Chaos Dashboard UI."
