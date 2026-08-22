#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="kyverno"

echo "Kyverno Status:"
echo "==============="
echo ""

if ! kubectl get namespace $NAMESPACE &>/dev/null; then
  echo "Namespace '$NAMESPACE' not found — Kyverno not installed"
  exit 1
fi

echo "Pods:"
kubectl get pods -n $NAMESPACE

echo ""
echo "Services:"
kubectl get svc -n $NAMESPACE

echo ""
echo "Cluster Policies:"
kubectl get clusterpolicies 2>/dev/null || echo "  No cluster policies found"

echo ""
echo "Policy Reports (summary):"
kubectl get policyreports -A --no-headers 2>/dev/null | wc -l | xargs -I{} echo "  {} policy reports across all namespaces"

echo ""
echo "Cluster Policy Reports (summary):"
kubectl get clusterpolicyreports --no-headers 2>/dev/null | wc -l | xargs -I{} echo "  {} cluster policy reports"
