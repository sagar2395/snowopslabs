#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="ingress-nginx"

echo "=== Nginx Ingress Controller Status ==="
echo ""

# Check namespace
if ! kubectl get namespace $NAMESPACE >/dev/null 2>&1; then
  echo "Nginx Ingress Controller is not installed (namespace $NAMESPACE not found)"
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
kubectl get ingressclass nginx 2>/dev/null || echo "  IngressClass 'nginx' not found"
echo ""

# Ingresses using this controller
echo "Ingresses using nginx class:"
kubectl get ingress --all-namespaces -o wide 2>/dev/null | grep -E "nginx|NAMESPACE" || echo "  No ingresses found"
echo ""

# Controller version
echo "Controller version:"
kubectl get deployment ingress-nginx-controller -n $NAMESPACE -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "  Not available"
echo ""
