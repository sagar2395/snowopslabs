#!/usr/bin/env bash
set -euo pipefail

NAMESPACE=traefik

echo "=== Traefik Ingress Status ==="
echo ""

# Check namespace
if ! kubectl get namespace $NAMESPACE >/dev/null 2>&1; then
  echo "Traefik is not installed (namespace $NAMESPACE not found)"
  exit 0
fi

# Pods
echo "Pods:"
kubectl get pods -n $NAMESPACE -o wide 2>/dev/null || echo "  No pods found"
echo ""

# Services
echo "Services:"
kubectl get svc -n $NAMESPACE 2>/dev/null || echo "  No services found"
echo ""

# IngressClass
echo "IngressClass:"
kubectl get ingressclass traefik 2>/dev/null || echo "  IngressClass 'traefik' not found"
echo ""

# Ingresses using this controller
echo "Ingresses using traefik class:"
kubectl get ingress --all-namespaces -o wide 2>/dev/null | grep -E "traefik|NAMESPACE" || echo "  No ingresses found"
echo ""

# Dashboard
echo "Dashboard:"
echo "  kubectl port-forward -n $NAMESPACE svc/traefik 9000:9000"
echo "  Then open http://localhost:9000/dashboard/"
