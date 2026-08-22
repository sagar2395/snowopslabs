#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="cert-manager"

echo "cert-manager Status:"
echo "===================="
echo ""

if ! kubectl get namespace $NAMESPACE &>/dev/null; then
  echo "Namespace '$NAMESPACE' not found — cert-manager not installed"
  exit 1
fi

echo "Pods:"
kubectl get pods -n $NAMESPACE

echo ""
echo "ClusterIssuers:"
kubectl get clusterissuers 2>/dev/null || echo "  No ClusterIssuers found"

echo ""
echo "Certificates (all namespaces):"
kubectl get certificates -A 2>/dev/null || echo "  No Certificates found"

echo ""
echo "CertificateRequests (all namespaces):"
kubectl get certificaterequests -A --no-headers 2>/dev/null | wc -l | xargs -I{} echo "  {} certificate requests"
