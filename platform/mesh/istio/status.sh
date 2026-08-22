#!/usr/bin/env bash
set -euo pipefail

SYSTEM_NS="istio-system"
MESH_NAMESPACE="${MESH_NAMESPACE:-go-api}"

echo "=== Istio Service Mesh Status ==="
echo ""

if ! kubectl get namespace "$SYSTEM_NS" >/dev/null 2>&1; then
  echo "Istio is not installed (namespace $SYSTEM_NS not found)"
  exit 0
fi

# Control plane health
echo "Control plane (istiod):"
if kubectl get deployment istiod -n "$SYSTEM_NS" >/dev/null 2>&1; then
  ready=$(kubectl get deployment istiod -n "$SYSTEM_NS" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
  desired=$(kubectl get deployment istiod -n "$SYSTEM_NS" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)
  echo "  istiod: ${ready:-0}/${desired:-0} ready"
else
  echo "  istiod deployment not found"
fi
echo ""

echo "Pods:"
kubectl get pods -n "$SYSTEM_NS" -o wide 2>/dev/null || echo "  No pods found"
echo ""

# Injection enrolment
echo "Meshed namespaces (istio-injection=enabled):"
kubectl get namespace -l istio-injection=enabled --no-headers 2>/dev/null | awk '{print "  "$1}' || echo "  (none)"
echo ""

# Sidecar visibility in the target namespace
echo "Sidecars in '$MESH_NAMESPACE':"
if kubectl get namespace "$MESH_NAMESPACE" >/dev/null 2>&1; then
  found=0
  for pod in $(kubectl get pods -n "$MESH_NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    if kubectl get pod "$pod" -n "$MESH_NAMESPACE" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null | grep -qw istio-proxy; then
      echo "  $pod: istio-proxy present"
      found=1
    fi
  done
  [ "$found" -eq 0 ] && echo "  No injected pods found (deploy a workload, then it gets a sidecar)"
else
  echo "  namespace '$MESH_NAMESPACE' not found"
fi
