#!/usr/bin/env bash
set -euo pipefail

# Remove the Postgres cluster, CNPG operator, and PVCs. Idempotent.

NAMESPACE="${POSTGRES_NAMESPACE:-postgres}"
OPERATOR_NS="cnpg-system"
CLUSTER="${POSTGRES_CLUSTER:-lab-postgres}"

echo "Uninstalling Postgres (namespace=${NAMESPACE})..."

if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  # 1. Delete the Cluster CR so the operator removes its pods first.
  kubectl delete cluster "$CLUSTER" -n "$NAMESPACE" --ignore-not-found --timeout=120s || true

  # 2. Drop the cluster's PVCs (CNPG keeps them by default for safety).
  kubectl delete pvc -n "$NAMESPACE" -l cnpg.io/cluster="$CLUSTER" --ignore-not-found || true

  # 3. Namespace cleanup (postgres has its own namespace).
  kubectl delete namespace "$NAMESPACE" --timeout=60s || true
fi

# 4. Remove the operator (cluster-scoped, in its own namespace).
if helm status cnpg -n "$OPERATOR_NS" >/dev/null 2>&1; then
  helm uninstall cnpg -n "$OPERATOR_NS"
fi
if kubectl get namespace "$OPERATOR_NS" >/dev/null 2>&1; then
  kubectl delete namespace "$OPERATOR_NS" --timeout=60s || true
fi

echo ""
echo "Postgres uninstalled."
