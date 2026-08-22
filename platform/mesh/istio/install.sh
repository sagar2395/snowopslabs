#!/usr/bin/env bash
set -euo pipefail

# Istio service mesh (sidecar mode) installer.
# Selected via MESH_PROVIDER=istio. Portable + idempotent.
#
# Config (env, with defaults — scripts never source .env themselves):
#   ISTIO_VERSION   pinned chart/app version (see versions.env)
#   MESH_NAMESPACE  workload namespace to enrol into the mesh (default: go-api)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SYSTEM_NS="istio-system"
ISTIO_VERSION="${ISTIO_VERSION:-1.24.2}"
MESH_NAMESPACE="${MESH_NAMESPACE:-go-api}"

echo "Installing Istio ${ISTIO_VERSION} (sidecar mode)..."

helm repo add istio https://istio-release.storage.googleapis.com/charts --force-update
helm repo update istio

# 1. CRDs + cluster resources (istio/base). Idempotent via upgrade --install.
helm upgrade --install istio-base istio/base \
  --namespace "$SYSTEM_NS" \
  --create-namespace \
  --version "$ISTIO_VERSION" \
  --set defaultRevision=default \
  --wait --timeout 5m

# 2. Control plane (istiod). Minimal resources for k3d (see values.yaml).
helm upgrade --install istiod istio/istiod \
  --namespace "$SYSTEM_NS" \
  --version "$ISTIO_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

echo "Waiting for istiod to be ready..."
kubectl rollout status deployment/istiod -n "$SYSTEM_NS" --timeout=120s

# 3. Enrol the workload namespace: sidecar auto-injection via namespace label.
if kubectl get namespace "$MESH_NAMESPACE" >/dev/null 2>&1; then
  echo "Enrolling namespace '$MESH_NAMESPACE' into the mesh (istio-injection=enabled)..."
  kubectl label namespace "$MESH_NAMESPACE" istio-injection=enabled --overwrite
  # Restart existing workloads so the sidecar is injected. Safe no-op if none.
  if kubectl get deployments -n "$MESH_NAMESPACE" --no-headers 2>/dev/null | grep -q .; then
    echo "Restarting deployments in '$MESH_NAMESPACE' to inject sidecars..."
    kubectl rollout restart deployment -n "$MESH_NAMESPACE"
    kubectl rollout status deployment -n "$MESH_NAMESPACE" --timeout=120s || true
  fi
else
  echo "Namespace '$MESH_NAMESPACE' not found yet."
  echo "Create it and deploy your app, then re-run to inject sidecars:"
  echo "    kubectl create namespace $MESH_NAMESPACE"
  echo "    kubectl label namespace $MESH_NAMESPACE istio-injection=enabled"
fi

echo ""
echo "Istio installed successfully."
echo "    Control plane: namespace '$SYSTEM_NS'"
echo "    Meshed namespace: '$MESH_NAMESPACE' (label istio-injection=enabled)"
echo "    Verify a sidecar: kubectl get pod -n $MESH_NAMESPACE -o jsonpath='{.items[0].spec.containers[*].name}'"
