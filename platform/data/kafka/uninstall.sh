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

# 5. CRDs. Neither `helm uninstall` nor namespace deletion removes them, and
# they are cluster-scoped, so without this the component is not actually
# uninstalled: the next install inherits CRDs whose status.storedVersions still
# names an API version the new chart may no longer serve, and Kubernetes then
# refuses to update them at all. Set KAFKA_KEEP_CRDS=true to keep them.
if [ "${KAFKA_KEEP_CRDS:-false}" != "true" ]; then
  echo "Removing Strimzi CRDs (cluster-scoped; Helm never deletes them)..."
  kubectl delete crd -l app=strimzi --ignore-not-found --timeout=120s || true
  # Older releases did not label every CRD, so sweep by name as well.
  for crd in $(kubectl get crd -o name 2>/dev/null | grep -E 'strimzi\.io$' || true); do
    kubectl delete "$crd" --ignore-not-found --timeout=60s || true
  done
fi

echo ""
echo "Kafka uninstalled."
