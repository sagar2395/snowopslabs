#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="kubernetes-dashboard"

echo "==> Uninstalling Kubernetes Dashboard..."

helm uninstall kubernetes-dashboard --namespace "$NAMESPACE" 2>/dev/null || true
kubectl delete namespace "$NAMESPACE" --ignore-not-found --timeout=60s

echo "==> Kubernetes Dashboard removed."
