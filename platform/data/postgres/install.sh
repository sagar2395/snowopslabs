#!/usr/bin/env bash
set -euo pipefail

# PostgreSQL via the CloudNativePG (CNPG) operator — a small 2-instance HA
# cluster whose built-in failover is a ready-made day-2 drill.
# Sub-component of the `data` category: install/remove independently of kafka.
# Portable + idempotent.
#
# Config (env, with defaults — scripts never source .env themselves):
#   CNPG_CHART_VERSION  pinned cloudnative-pg chart version (versions.env)
#   POSTGRES_NAMESPACE  namespace for the Postgres Cluster CR (default: postgres)
#   POSTGRES_CLUSTER    Cluster CR name (default: lab-postgres)
#   POSTGRES_INSTANCES  number of instances (default: 2 — 1 primary + 1 replica)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="${POSTGRES_NAMESPACE:-postgres}"
OPERATOR_NS="cnpg-system"
CLUSTER="${POSTGRES_CLUSTER:-lab-postgres}"
INSTANCES="${POSTGRES_INSTANCES:-2}"
CNPG_CHART_VERSION="${CNPG_CHART_VERSION:-0.23.0}"

echo "Installing CloudNativePG operator (chart ${CNPG_CHART_VERSION})..."

helm repo add cnpg https://cloudnative-pg.github.io/charts --force-update
helm repo update cnpg

# Operator (cluster-scoped, watches all namespaces). Idempotent.
helm upgrade --install cnpg cnpg/cloudnative-pg \
  --namespace "$OPERATOR_NS" \
  --create-namespace \
  --version "$CNPG_CHART_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

echo "Waiting for the CNPG operator to be ready..."
kubectl rollout status deployment/cnpg-cloudnative-pg -n "$OPERATOR_NS" --timeout=180s

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Postgres cluster: 2 instances (1 primary + 1 replica), small storage.
echo "Applying Postgres cluster '$CLUSTER' (${INSTANCES} instances)..."
cat <<EOF | kubectl apply -f -
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: ${CLUSTER}
  namespace: ${NAMESPACE}
spec:
  instances: ${INSTANCES}
  storage:
    size: 1Gi
  resources:
    requests:
      memory: 256Mi
      cpu: 100m
    limits:
      memory: 512Mi
      cpu: 500m
EOF

echo "Waiting for Postgres cluster to become Ready..."
# CNPG sets a Ready condition; fall back to polling readyInstances if absent.
if ! kubectl wait cluster/"$CLUSTER" -n "$NAMESPACE" --for=condition=Ready --timeout=300s 2>/dev/null; then
  echo "Polling readyInstances..."
  for _ in $(seq 1 60); do
    ready=$(kubectl get cluster "$CLUSTER" -n "$NAMESPACE" -o jsonpath='{.status.readyInstances}' 2>/dev/null || echo 0)
    [ "${ready:-0}" = "$INSTANCES" ] && break
    sleep 5
  done
fi

echo ""
echo "Postgres installed successfully."
echo "    Cluster: '$CLUSTER' in namespace '$NAMESPACE' (${INSTANCES} instances)"
echo "    Service (read-write/primary): ${CLUSTER}-rw.${NAMESPACE}.svc:5432"
echo "    App credentials secret: ${CLUSTER}-app (keys: username, password, dbname)"
echo "    Failover drill — see docs/runbooks/10-stack-expansion.md"
