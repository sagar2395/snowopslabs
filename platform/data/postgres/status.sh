#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${POSTGRES_NAMESPACE:-postgres}"
OPERATOR_NS="cnpg-system"
CLUSTER="${POSTGRES_CLUSTER:-lab-postgres}"

echo "=== Postgres (CloudNativePG) Status ==="
echo ""

# Operator health (cluster-scoped, separate namespace)
echo "Operator:"
if kubectl get deployment cnpg-cloudnative-pg -n "$OPERATOR_NS" >/dev/null 2>&1; then
  ready=$(kubectl get deployment cnpg-cloudnative-pg -n "$OPERATOR_NS" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
  desired=$(kubectl get deployment cnpg-cloudnative-pg -n "$OPERATOR_NS" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)
  echo "  cnpg-cloudnative-pg: ${ready:-0}/${desired:-0} ready"
else
  echo "  operator deployment not found"
fi
echo ""

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "Postgres cluster is not installed (namespace $NAMESPACE not found)"
  exit 0
fi

# Cluster CR readiness (portable jsonpath)
echo "Cluster '$CLUSTER':"
if kubectl get cluster "$CLUSTER" -n "$NAMESPACE" >/dev/null 2>&1; then
  instances=$(kubectl get cluster "$CLUSTER" -n "$NAMESPACE" -o jsonpath='{.spec.instances}' 2>/dev/null || echo "?")
  ready=$(kubectl get cluster "$CLUSTER" -n "$NAMESPACE" -o jsonpath='{.status.readyInstances}' 2>/dev/null || echo 0)
  phase=$(kubectl get cluster "$CLUSTER" -n "$NAMESPACE" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
  primary=$(kubectl get cluster "$CLUSTER" -n "$NAMESPACE" -o jsonpath='{.status.currentPrimary}' 2>/dev/null || echo "")
  echo "  Ready instances: ${ready:-0}/${instances}"
  echo "  Phase: ${phase:-Unknown}"
  echo "  Current primary: ${primary:-Unknown}"
else
  echo "  Cluster CR not found"
fi
echo ""

# Per-pod roles (primary vs replica) — useful for watching a failover
echo "Instances (role):"
for pod in $(kubectl get pods -n "$NAMESPACE" -l cnpg.io/cluster="$CLUSTER" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
  role=$(kubectl get pod "$pod" -n "$NAMESPACE" -o jsonpath='{.metadata.labels.cnpg\.io/instanceRole}' 2>/dev/null || echo "")
  echo "  $pod: ${role:-unknown}"
done
echo ""

echo "Connect (psql):"
echo "  kubectl -n $NAMESPACE exec -it ${CLUSTER}-1 -- psql -U postgres"
echo "Failover drill:"
echo "  kubectl -n $NAMESPACE delete pod \$(kubectl -n $NAMESPACE get pod -l cnpg.io/instanceRole=primary -o name)"
echo "  watch kubectl -n $NAMESPACE get pods -l cnpg.io/cluster=$CLUSTER"
