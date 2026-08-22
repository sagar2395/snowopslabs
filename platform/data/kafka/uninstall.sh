#!/usr/bin/env bash
set -euo pipefail

# Remove the Kafka cluster, Strimzi operator, and leftover state. Idempotent.

NAMESPACE="${KAFKA_NAMESPACE:-kafka}"
CLUSTER="${KAFKA_CLUSTER:-lab-kafka}"

echo "Uninstalling Kafka (namespace=${NAMESPACE})..."

if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  # 1. Delete the CRs first so the operator tears the broker down cleanly.
  kubectl delete kafka "$CLUSTER" -n "$NAMESPACE" --ignore-not-found --timeout=120s || true
  kubectl delete kafkanodepool dual-role -n "$NAMESPACE" --ignore-not-found --timeout=60s || true

  # 2. Remove the operator (Helm release).
  if helm status strimzi-kafka-operator -n "$NAMESPACE" >/dev/null 2>&1; then
    helm uninstall strimzi-kafka-operator -n "$NAMESPACE"
  fi

  # 3. Drop any PVCs Strimzi created (ephemeral storage leaves none, but be safe).
  kubectl delete pvc -n "$NAMESPACE" -l strimzi.io/cluster="$CLUSTER" --ignore-not-found || true

  # 4. Namespace cleanup (kafka has its own namespace).
  kubectl delete namespace "$NAMESPACE" --timeout=60s || true
fi

echo ""
echo "Kafka uninstalled."
