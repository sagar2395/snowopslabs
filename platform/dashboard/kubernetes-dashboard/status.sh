#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="kubernetes-dashboard"

echo "==> Kubernetes Dashboard Status"
echo ""

if kubectl get namespace "$NAMESPACE" &>/dev/null; then
  echo "Namespace: $NAMESPACE (exists)"
  echo ""
  kubectl get pods -n "$NAMESPACE"
  echo ""
  kubectl get svc -n "$NAMESPACE"
else
  echo "Kubernetes Dashboard is not installed."
fi
