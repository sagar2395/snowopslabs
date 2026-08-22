#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="opencost"

echo "=== OpenCost Cost Visibility Status ==="
echo ""

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "OpenCost is not installed (namespace ${NAMESPACE} not found)"
  exit 0
fi

echo "Components:"
if kubectl get deployment opencost -n "$NAMESPACE" >/dev/null 2>&1; then
  ready=$(kubectl get deployment opencost -n "$NAMESPACE" \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
  desired=$(kubectl get deployment opencost -n "$NAMESPACE" \
    -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 1)
  echo "  opencost: ${ready:-0}/${desired:-0} ready"
else
  echo "  opencost deployment: not found"
fi
echo ""

echo "Services:"
kubectl get svc -n "$NAMESPACE" 2>/dev/null || true
echo ""

echo "Ingress:"
kubectl get ingress -n "$NAMESPACE" 2>/dev/null || echo "  (none — use port-forward to access the UI)"
echo ""

DOMAIN="${DOMAIN_SUFFIX:-k3d.local}"
echo "Access:"
echo "  http://opencost.${DOMAIN}  (if ingress is installed)"
echo "  kubectl -n ${NAMESPACE} port-forward svc/opencost 9090 &"
echo "  then: http://localhost:9090"
echo ""
echo "NOTE: k3d has no real cloud billing — OpenCost uses on-prem pricing defaults."
echo "      Per-namespace cost estimates are still useful for right-sizing exercises."
