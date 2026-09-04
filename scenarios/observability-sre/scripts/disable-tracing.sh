#!/usr/bin/env bash
set -euo pipefail

# Reverse of enable-tracing.sh: unset the OTLP endpoint so the app stops
# exporting when the scenario is torn down and Alloy goes away. Without this the
# app retries every export against a Service that no longer exists.

NAMESPACE="${GO_API_NAMESPACE:-go-api}"
DEPLOYMENT="go-api"

if ! kubectl get deployment "$DEPLOYMENT" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "Deployment ${DEPLOYMENT} not found in ${NAMESPACE}; nothing to unset."
  exit 0
fi

echo "Removing the OTLP endpoint from ${DEPLOYMENT}..."
kubectl set env "deployment/${DEPLOYMENT}" -n "$NAMESPACE" \
  OTEL_EXPORTER_OTLP_ENDPOINT- OTEL_SERVICE_NAME-
kubectl rollout status "deployment/${DEPLOYMENT}" -n "$NAMESPACE" --timeout=120s || true
