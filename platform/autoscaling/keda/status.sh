#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="keda"

echo "=== KEDA Autoscaling Status ==="
echo ""

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "KEDA is not installed (namespace $NAMESPACE not found)"
  exit 0
fi

# Control-plane health
echo "Components:"
for d in keda-operator keda-operator-metrics-apiserver keda-admission-webhooks; do
  if kubectl get deployment "$d" -n "$NAMESPACE" >/dev/null 2>&1; then
    ready=$(kubectl get deployment "$d" -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
    desired=$(kubectl get deployment "$d" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)
    echo "  $d: ${ready:-0}/${desired:-0} ready"
  fi
done
echo ""

# CRDs present?
echo "CRDs:"
if kubectl get crd scaledobjects.keda.sh >/dev/null 2>&1; then
  echo "  scaledobjects.keda.sh present"
else
  echo "  scaledobjects.keda.sh missing"
fi
echo ""

# ScaledObjects across the cluster, with the HPA they manage and current replicas.
echo "ScaledObjects (all namespaces):"
if kubectl get scaledobject -A --no-headers 2>/dev/null | grep -q .; then
  kubectl get scaledobject -A 2>/dev/null
  echo ""
  echo "Managed HPAs:"
  kubectl get hpa -A 2>/dev/null | grep -E "keda-hpa|NAMESPACE" || echo "  (none yet)"
else
  echo "  (none — define one with a ScaledObject; see the autoscaling-under-load scenario)"
fi
