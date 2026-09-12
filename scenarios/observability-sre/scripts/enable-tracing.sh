#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

# Point the workload at the Alloy collector. This is the wiring step the collector
# pattern is about: the app exports OTLP to a collector it can reach and never
# needs to know which trace backend is behind it.
#
# `kubectl set env` patches the Deployment's pod template, which rolls the pods
# — the same mechanism as any other config change.

NAMESPACE="${WORKLOAD_NAMESPACE}"
MONITORING_NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
DEPLOYMENT="${WORKLOAD_NAME}"
ENDPOINT="http://alloy.${MONITORING_NAMESPACE}.svc.cluster.local:4318"

if ! kubectl get deployment "$DEPLOYMENT" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "Deployment ${DEPLOYMENT} not found in namespace ${NAMESPACE}." >&2
  echo "Deploy the app first: labctl app deploy ${WORKLOAD_NAME}" >&2
  exit 1
fi

echo "Pointing ${DEPLOYMENT} at the Alloy collector (${ENDPOINT})..."
kubectl set env "deployment/${DEPLOYMENT}" -n "$NAMESPACE" \
  OTEL_EXPORTER_OTLP_ENDPOINT="$ENDPOINT" \
  OTEL_SERVICE_NAME="$DEPLOYMENT"

kubectl rollout status "deployment/${DEPLOYMENT}" -n "$NAMESPACE" --timeout=120s

echo ""
echo "Tracing enabled. Every request now produces a span, and each access-log"
echo "line carries the trace_id that links it to that span."
echo "  Generate traffic:  labctl traffic start --app ${WORKLOAD_NAME} --profile steady --rps 25"
echo "  See the trace ID:  kubectl -n ${NAMESPACE} logs deploy/${DEPLOYMENT} | grep trace_id | tail -1"
