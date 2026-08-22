#!/usr/bin/env bash
set -euo pipefail

SYSTEM_NS="linkerd"
MESH_NAMESPACE="${MESH_NAMESPACE:-go-api}"

echo "=== Linkerd Service Mesh Status ==="
echo ""

if ! kubectl get namespace "$SYSTEM_NS" >/dev/null 2>&1; then
  echo "Linkerd is not installed (namespace $SYSTEM_NS not found)"
  exit 0
fi

# Control plane health
echo "Control plane:"
found=0
for d in linkerd-destination linkerd-identity linkerd-proxy-injector; do
  if kubectl get deployment "$d" -n "$SYSTEM_NS" >/dev/null 2>&1; then
    ready=$(kubectl get deployment "$d" -n "$SYSTEM_NS" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    desired=$(kubectl get deployment "$d" -n "$SYSTEM_NS" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)
    echo "  $d: ${ready:-0}/${desired:-0} ready"
    found=1
  fi
done
[ "$found" -eq 0 ] && echo "  No control-plane deployments found"
echo ""

echo "Pods:"
kubectl get pods -n "$SYSTEM_NS" -o wide 2>/dev/null || echo "  No pods found"
echo ""

# Injection enrolment
echo "Meshed namespaces (linkerd.io/inject=enabled):"
ns_found=0
for ns in $(kubectl get namespace -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
  if kubectl get namespace "$ns" -o jsonpath='{.metadata.annotations.linkerd\.io/inject}' 2>/dev/null | grep -q enabled; then
    echo "  $ns"
    ns_found=1
  fi
done
[ "$ns_found" -eq 0 ] && echo "  (none)"
echo ""

# Proxy visibility in the target namespace
echo "Proxies in '$MESH_NAMESPACE':"
if kubectl get namespace "$MESH_NAMESPACE" >/dev/null 2>&1; then
  p_found=0
  for pod in $(kubectl get pods -n "$MESH_NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    if kubectl get pod "$pod" -n "$MESH_NAMESPACE" -o jsonpath='{.spec.containers[*].name}' 2>/dev/null | grep -qw linkerd-proxy; then
      echo "  $pod: linkerd-proxy present"
      p_found=1
    fi
  done
  [ "$p_found" -eq 0 ] && echo "  No injected pods found (deploy a workload, then it gets a proxy)"
else
  echo "  namespace '$MESH_NAMESPACE' not found"
fi
