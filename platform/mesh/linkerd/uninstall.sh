#!/usr/bin/env bash
set -euo pipefail

# Remove Linkerd and un-enrol the workload namespace. Idempotent.

SYSTEM_NS="linkerd"
MESH_NAMESPACE="${MESH_NAMESPACE:-go-api}"

echo "Uninstalling Linkerd..."

# 1. Un-enrol the workload namespace and drop injected proxies by restarting.
if kubectl get namespace "$MESH_NAMESPACE" >/dev/null 2>&1; then
  if kubectl get namespace "$MESH_NAMESPACE" -o jsonpath='{.metadata.annotations.linkerd\.io/inject}' 2>/dev/null | grep -q enabled; then
    echo "Un-enrolling namespace '$MESH_NAMESPACE' (removing linkerd.io/inject annotation)..."
    kubectl annotate namespace "$MESH_NAMESPACE" linkerd.io/inject- 2>/dev/null || true
    if kubectl get deployments -n "$MESH_NAMESPACE" --no-headers 2>/dev/null | grep -q .; then
      echo "Restarting deployments in '$MESH_NAMESPACE' to drop proxies..."
      kubectl rollout restart deployment -n "$MESH_NAMESPACE" || true
    fi
  fi
fi

# 2. Control plane then CRDs (reverse install order).
if helm status linkerd-control-plane -n "$SYSTEM_NS" >/dev/null 2>&1; then
  helm uninstall linkerd-control-plane -n "$SYSTEM_NS"
fi
if helm status linkerd-crds -n "$SYSTEM_NS" >/dev/null 2>&1; then
  helm uninstall linkerd-crds -n "$SYSTEM_NS"
fi

# 3. Namespace cleanup.
if kubectl get namespace "$SYSTEM_NS" >/dev/null 2>&1; then
  kubectl delete namespace "$SYSTEM_NS" --timeout=60s || true
fi

echo ""
echo "Linkerd uninstalled."
