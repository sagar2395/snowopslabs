#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="argocd"

echo "ArgoCD Status:"
echo "=============="
echo ""

if ! kubectl get namespace $NAMESPACE &>/dev/null; then
  echo "Namespace '$NAMESPACE' not found"
  exit 1
fi

echo "Pods in '$NAMESPACE' namespace:"
kubectl get pods -n $NAMESPACE

echo ""
echo "Services in '$NAMESPACE' namespace:"
kubectl get svc -n $NAMESPACE

echo ""
echo "ArgoCD Ingress:"
kubectl get ingress -n $NAMESPACE

echo ""
echo "ArgoCD server rollout status:"
kubectl rollout status deployment/argocd-server -n $NAMESPACE --timeout=30s || true

echo ""
echo "ArgoCD Access:"
echo "  - External: http://argocd.${DOMAIN_SUFFIX:-k3d.local}"
echo "  - Internal: http://argocd-server.$NAMESPACE.svc.cluster.local:80"
echo ""
echo "Retrieve initial admin password:"
echo "  kubectl -n $NAMESPACE get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d"
