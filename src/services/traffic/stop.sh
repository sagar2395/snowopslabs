#!/usr/bin/env bash
# Stop the traffic generator and remove everything it created.
# The namespace is dedicated to the generator, so full cleanup deletes it.
set -euo pipefail

NAMESPACE="${TRAFFIC_NAMESPACE:-traffic}"

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "Traffic generator is not running (namespace '$NAMESPACE' not found)."
  exit 0
fi

echo "Stopping traffic generator..."
kubectl delete job traffic-k6 --namespace "$NAMESPACE" --ignore-not-found --wait=true
kubectl delete configmap traffic-profile --namespace "$NAMESPACE" --ignore-not-found
kubectl delete namespace "$NAMESPACE" --ignore-not-found

echo "Traffic generator stopped and cleaned up."
