#!/usr/bin/env bash
set -euo pipefail

# Kafka via the Strimzi operator (KRaft, single combined node — k3d-minimal).
# Sub-component of the `data` category: install/remove independently of postgres.
# Portable + idempotent.
#
# Config (env, with defaults — scripts never source .env themselves):
#   STRIMZI_VERSION   pinned strimzi-kafka-operator chart version (versions.env)
#   KAFKA_VERSION     Kafka broker version the operator should run
#   KAFKA_NAMESPACE   namespace for the operator + Kafka CR (default: kafka)
#   KAFKA_CLUSTER     Kafka CR name (default: lab-kafka)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="${KAFKA_NAMESPACE:-kafka}"
CLUSTER="${KAFKA_CLUSTER:-lab-kafka}"
STRIMZI_VERSION="${STRIMZI_VERSION:-0.45.0}"
KAFKA_VERSION="${KAFKA_VERSION:-3.9.0}"

echo "Installing Strimzi Kafka operator ${STRIMZI_VERSION} (Kafka ${KAFKA_VERSION}, namespace=${NAMESPACE})..."

helm repo add strimzi https://strimzi.io/charts/ --force-update
helm repo update strimzi

# Operator (installs CRDs). Watches its own namespace by default, which is where
# we create the Kafka CR below. Idempotent via upgrade --install.
helm upgrade --install strimzi-kafka-operator strimzi/strimzi-kafka-operator \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$STRIMZI_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

echo "Waiting for the Strimzi operator to be ready..."
kubectl rollout status deployment/strimzi-cluster-operator -n "$NAMESPACE" --timeout=180s

# Kafka cluster: KRaft mode, one dual-role node, ephemeral storage (fits k3d).
echo "Applying Kafka cluster '$CLUSTER' (KRaft, 1 node, ephemeral)..."
cat <<EOF | kubectl apply -f -
apiVersion: kafka.strimzi.io/v1beta2
kind: KafkaNodePool
metadata:
  name: dual-role
  namespace: ${NAMESPACE}
  labels:
    strimzi.io/cluster: ${CLUSTER}
spec:
  replicas: 1
  roles:
    - controller
    - broker
  storage:
    type: ephemeral
---
apiVersion: kafka.strimzi.io/v1beta2
kind: Kafka
metadata:
  name: ${CLUSTER}
  namespace: ${NAMESPACE}
  annotations:
    strimzi.io/node-pools: enabled
    strimzi.io/kraft: enabled
spec:
  kafka:
    version: ${KAFKA_VERSION}
    listeners:
      - name: plain
        port: 9092
        type: internal
        tls: false
    config:
      offsets.topic.replication.factor: 1
      transaction.state.log.replication.factor: 1
      transaction.state.log.min.isr: 1
      default.replication.factor: 1
      min.insync.replicas: 1
    resources:
      requests:
        memory: 512Mi
        cpu: 250m
      limits:
        memory: 1Gi
        cpu: "1"
  entityOperator:
    topicOperator: {}
    userOperator: {}
EOF

echo "Waiting for Kafka cluster to become Ready (this provisions the broker)..."
kubectl wait kafka/"$CLUSTER" -n "$NAMESPACE" --for=condition=Ready --timeout=300s

echo ""
echo "Kafka installed successfully."
echo "    Cluster: '$CLUSTER' in namespace '$NAMESPACE'"
echo "    Bootstrap (in-cluster): ${CLUSTER}-kafka-bootstrap.${NAMESPACE}.svc:9092"
echo "    Smoke test (produce/consume) — see docs/runbooks/10-stack-expansion.md"
