#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="pager"

if ! kubectl get deploy pager -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "Pager: not installed"
  exit 0
fi

kubectl get deploy,pods -n "$NAMESPACE" -l app=pager 2>/dev/null || kubectl get deploy pager -n "$NAMESPACE"
echo ""
echo "Recent pages:"
kubectl logs deploy/pager -n "$NAMESPACE" --tail=20 2>/dev/null | grep '^PAGE' || echo "  (no pages received yet)"
