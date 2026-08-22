#!/usr/bin/env bash
# Show the traffic generator's current state and recent k6 output.
set -euo pipefail

NAMESPACE="${TRAFFIC_NAMESPACE:-traffic}"

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "Traffic generator: not running"
  exit 0
fi

if ! kubectl get job traffic-k6 --namespace "$NAMESPACE" >/dev/null 2>&1; then
  echo "Traffic generator: not running (namespace '$NAMESPACE' exists but no job)"
  exit 0
fi

PROFILE="$(kubectl get job traffic-k6 --namespace "$NAMESPACE" \
  -o jsonpath='{.metadata.labels.traffic-profile}' 2>/dev/null || true)"
echo "Traffic generator: running (profile: ${PROFILE:-unknown})"
echo ""
kubectl get job traffic-k6 --namespace "$NAMESPACE"
echo ""
kubectl get pods --namespace "$NAMESPACE" -l app=traffic-k6
echo ""
echo "Recent k6 output:"
kubectl logs job/traffic-k6 --namespace "$NAMESPACE" --tail=15 2>/dev/null || echo "  (no logs yet)"
