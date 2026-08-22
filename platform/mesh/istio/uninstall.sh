#!/usr/bin/env bash
set -euo pipefail

# Remove Istio and un-enrol the workload namespace. Idempotent.

SYSTEM_NS="istio-system"
MESH_NAMESPACE="${MESH_NAMESPACE:-go-api}"

echo "Uninstalling Istio..."

# 1. Un-enrol the workload namespace and strip injected sidecars by restarting.
if kubectl get namespace "$MESH_NAMESPACE" >/dev/null 2>&1; then
  if kubectl get namespace "$MESH_NAMESPACE" -o jsonpath='{.metadata.labels.istio-injection}' 2>/dev/null | grep -q enabled; then
    echo "Un-enrolling namespace '$MESH_NAMESPACE' (removing istio-injection label)..."
    kubectl label namespace "$MESH_NAMESPACE" istio-injection- --overwrite 2>/dev/null || true
    if kubectl get deployments -n "$MESH_NAMESPACE" --no-headers 2>/dev/null | grep -q .; then
      echo "Restarting deployments in '$MESH_NAMESPACE' to drop sidecars..."
      kubectl rollout restart deployment -n "$MESH_NAMESPACE" || true
    fi
  fi
fi

# 2. Control plane then CRDs (reverse install order).
if helm status istiod -n "$SYSTEM_NS" >/dev/null 2>&1; then
  helm uninstall istiod -n "$SYSTEM_NS"
fi
if helm status istio-base -n "$SYSTEM_NS" >/dev/null 2>&1; then
  helm uninstall istio-base -n "$SYSTEM_NS"
fi

# 3. Namespace cleanup.
if kubectl get namespace "$SYSTEM_NS" >/dev/null 2>&1; then
  kubectl delete namespace "$SYSTEM_NS" --timeout=60s || true
fi

echo ""
echo "Istio uninstalled."
