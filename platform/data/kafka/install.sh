#!/usr/bin/env bash
set -euo pipefail

# Kafka via the Strimzi operator (KRaft, single combined node — k3d-minimal).
# Sub-component of the `data` category: install/remove independently of postgres.
# Portable + idempotent.
#
# Config (env, with defaults — scripts never source .env themselves):
#   STRIMZI_VERSION   pinned strimzi-kafka-operator chart version (config/versions.env)
#   KAFKA_VERSION     Kafka broker version the operator should run
#   KAFKA_NAMESPACE   namespace for the operator + Kafka CR (default: kafka)
#   KAFKA_CLUSTER     Kafka CR name (default: lab-kafka)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="${KAFKA_NAMESPACE:-kafka}"
CLUSTER="${KAFKA_CLUSTER:-lab-kafka}"
# Strimzi 1.x is KRaft-only (ZooKeeper support is gone), which suits the CRs
# below — they already use KRaft + KafkaNodePool. The broker version MUST be one
# the operator ships an image for: 1.2.0 supports Kafka 4.2.0-4.3.1 only, so a
# 3.x KAFKA_VERSION is rejected. Verify before bumping either:
#   helm template strimzi/strimzi-kafka-operator --version "$STRIMZI_VERSION" \
#     | grep -A6 STRIMZI_KAFKA_IMAGES
STRIMZI_VERSION="${STRIMZI_VERSION:-1.2.0}"
KAFKA_VERSION="${KAFKA_VERSION:-4.3.1}"

echo "Installing Strimzi Kafka operator ${STRIMZI_VERSION} (Kafka ${KAFKA_VERSION}, namespace=${NAMESPACE})..."

helm repo add strimzi https://strimzi.io/charts/ --force-update
helm repo update strimzi

# Helm installs a chart's CRDs on FIRST install only — `helm upgrade` never
# touches them. Strimzi 1.x serves StrimziPodSet at core.strimzi.io/v1 while
# 0.x served v1beta2, so upgrading the operator without this leaves it querying
# an API version the cluster does not have, and it crash-loops with
# "GET .../apis/core.strimzi.io/v1/... Not Found" while the Kafka CR sits
# unreconciled. Server-side apply is required: these CRDs exceed the annotation
# size limit that client-side apply uses.
# On a FIRST install Helm installs the crds/ directory itself and owns those
# fields, so leave them alone — applying them here first makes Helm's own install
# fail with a field-manager conflict on .spec.versions. Only an upgrade needs
# this, because `helm upgrade` never touches CRDs.
CRD_ERR="$(mktemp "${TMPDIR:-/tmp}/strimzi-crds.XXXXXX")"
if helm status strimzi-kafka-operator -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "Applying Strimzi ${STRIMZI_VERSION} CRDs (helm upgrade does not update CRDs)..."
  helm show crds strimzi/strimzi-kafka-operator --version "$STRIMZI_VERSION" \
    | kubectl apply --server-side --force-conflicts -f - 2>&1 | tee "$CRD_ERR" || true
fi

# Strimzi 1.x serves its CRDs at v1 only; 0.x stored objects as v1beta2.
# Kubernetes refuses to drop a version that is still in status.storedVersions
# until a storage migration has rewritten every existing object. There is no
# in-place path across that boundary, so say what to do instead of leaving ten
# CRD errors on screen.
if grep -q "must remain in spec.versions until a storage migration" "$CRD_ERR"; then
  rm -f "$CRD_ERR"
  cat >&2 <<'MSG'

Strimzi cannot be upgraded in place across the 0.x -> 1.x boundary.

  The installed CRDs still record v1beta2 as a stored version, and Strimzi 1.x
  ships v1 only. Kubernetes will not remove a stored version until every stored
  object has been migrated, and Strimzi provides no migration for this jump.

  This lab's Kafka uses ephemeral storage (the Kafka CR warns that a restart
  loses topic data), so the supported path is to recreate it:

    labctl platform down data/kafka
    labctl platform up data/kafka

  Any scenario using Kafka must be re-activated afterwards:

    labctl scenario up event-driven-arch --force

MSG
  exit 1
fi
rm -f "$CRD_ERR"

# Operator. Watches its own namespace by default, which is where we create the
# Kafka CR below. Idempotent via upgrade --install.
helm upgrade --install strimzi-kafka-operator strimzi/strimzi-kafka-operator \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$STRIMZI_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

echo "Waiting for the Strimzi operator to be ready..."
kubectl rollout status deployment/strimzi-cluster-operator -n "$NAMESPACE" --timeout=180s

# Broker metrics. A Kafka CR without spec.kafka.metricsConfig exposes NOTHING to
# Prometheus — the kafka-exporter only covers consumer-group lag and topic
# offsets, not broker health, throughput or under-replicated partitions.
echo "Applying the JMX Prometheus exporter rules for the broker..."
kubectl apply -n "$NAMESPACE" -f "$SCRIPT_DIR/jmx-exporter-config.yaml"

# Kafka cluster: KRaft mode, one dual-role node, ephemeral storage (fits k3d).
# apiVersion is kafka.strimzi.io/v1 — Strimzi 1.x dropped v1beta2 entirely.
echo "Applying Kafka cluster '$CLUSTER' (KRaft, 1 node, ephemeral)..."
cat <<EOF | kubectl apply -f -
apiVersion: kafka.strimzi.io/v1
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
  # Strimzi 1.x moved resources off spec.kafka and onto the node pool: the pool
  # is what owns the pods, so it owns their sizing.
  resources:
    requests:
      memory: 512Mi
      cpu: 250m
    limits:
      memory: 1Gi
      cpu: "1"
---
apiVersion: kafka.strimzi.io/v1
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
    metricsConfig:
      type: jmxPrometheusExporter
      valueFrom:
        configMapKeyRef:
          name: kafka-metrics
          key: kafka-metrics-config.yml
  entityOperator:
    topicOperator: {}
    userOperator: {}
  kafkaExporter:
    topicRegex: ".*"
    groupRegex: ".*"
EOF

echo "Waiting for Kafka cluster to become Ready (this provisions the broker)..."
kubectl wait kafka/"$CLUSTER" -n "$NAMESPACE" --for=condition=Ready --timeout=300s

echo ""
echo "Kafka installed successfully."
echo "    Cluster: '$CLUSTER' in namespace '$NAMESPACE'"
echo "    Bootstrap (in-cluster): ${CLUSTER}-kafka-bootstrap.${NAMESPACE}.svc:9092"
echo "    Smoke test (produce/consume) — see docs/runbooks/10-stack-expansion.md"
