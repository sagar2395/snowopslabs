#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="sealed-secrets"

echo "Sealed Secrets Status:"
echo "======================"
echo ""

if ! kubectl get namespace $NAMESPACE &>/dev/null; then
  echo "Namespace '$NAMESPACE' not found — Sealed Secrets not installed"
  exit 1
fi

echo "Pods:"
kubectl get pods -n $NAMESPACE

echo ""
echo "Services:"
kubectl get svc -n $NAMESPACE

echo ""
echo "Sealed Secrets (all namespaces):"
kubectl get sealedsecrets -A 2>/dev/null || echo "  No sealed secrets found"

echo ""
echo "Controller public key:"
kubeseal --controller-name=sealed-secrets --controller-namespace=$NAMESPACE --fetch-cert 2>/dev/null | head -1 || echo "  kubeseal CLI not installed (install to manage sealed secrets)"
