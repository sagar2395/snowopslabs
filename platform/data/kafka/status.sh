#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${KAFKA_NAMESPACE:-kafka}"
CLUSTER="${KAFKA_CLUSTER:-lab-kafka}"

echo "=== Kafka (Strimzi) Status ==="
echo ""

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "Kafka is not installed (namespace $NAMESPACE not found)"
  exit 0
fi

# Operator health
echo "Operator:"
if kubectl get deployment strimzi-cluster-operator -n "$NAMESPACE" >/dev/null 2>&1; then
  ready=$(kubectl get deployment strimzi-cluster-operator -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)
  desired=$(kubectl get deployment strimzi-cluster-operator -n "$NAMESPACE" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)
  echo "  strimzi-cluster-operator: ${ready:-0}/${desired:-0} ready"
else
  echo "  operator deployment not found"
fi
echo ""

# Kafka CR readiness condition (portable jsonpath, no text scraping)
echo "Kafka cluster '$CLUSTER':"
if kubectl get kafka "$CLUSTER" -n "$NAMESPACE" >/dev/null 2>&1; then
  cond=$(kubectl get kafka "$CLUSTER" -n "$NAMESPACE" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
  echo "  Ready: ${cond:-Unknown}"
  kver=$(kubectl get kafka "$CLUSTER" -n "$NAMESPACE" -o jsonpath='{.status.kafkaVersion}' 2>/dev/null || echo "")
  [ -n "$kver" ] && echo "  Kafka version: $kver"
else
  echo "  Kafka CR not found"
fi
echo ""

echo "Pods:"
kubectl get pods -n "$NAMESPACE" -o wide 2>/dev/null || echo "  No pods found"
echo ""

echo "Topics:"
kubectl get kafkatopic -n "$NAMESPACE" --no-headers 2>/dev/null | awk '{print "  "$1}' || echo "  (none)"
echo ""

echo "Smoke test (produce/consume) — run from inside the cluster:"
echo "  kubectl -n $NAMESPACE run kcat --rm -it --image=edenhill/kcat:1.7.1 --restart=Never -- \\"
echo "    -b ${CLUSTER}-kafka-bootstrap:9092 -t demo -P   # producer (Ctrl-D to send)"
