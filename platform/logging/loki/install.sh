#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOKI_VALUES_FILE="$(mktemp "${TMPDIR:-/tmp}/loki-values.XXXXXX")"
PROMTAIL_VALUES_FILE="$(mktemp "${TMPDIR:-/tmp}/promtail-values.XXXXXX")"
trap 'rm -f "$LOKI_VALUES_FILE" "$PROMTAIL_VALUES_FILE"' EXIT

# Log retention: default 7 days (168h). Set LOKI_RETENTION_HOURS to override.
# Set to 0 to disable retention (logs never expire).
RETENTION_HOURS="${LOKI_RETENTION_HOURS:-168}"
if [ "$RETENTION_HOURS" = "0" ]; then
  RETENTION_PERIOD="0s"
else
  RETENTION_PERIOD="${RETENTION_HOURS}h"
fi

echo "Installing Loki (single-binary mode, namespace=${NAMESPACE}, retention=${RETENTION_PERIOD})..."

helm repo add grafana https://grafana.github.io/helm-charts --force-update
helm repo update

# Clean up stuck pending releases if any
if helm status loki -n "$NAMESPACE" 2>/dev/null | grep -q "pending-"; then
  echo "Cleaning up stuck Loki release..."
  helm delete loki -n "$NAMESPACE" --wait 2>/dev/null || true
fi

# Install with runtime retention override. Using --set (not sed -i) keeps the
# script portable across macOS and Linux (avoids GNU-only sed -i behaviour).
helm upgrade --install loki grafana/loki \
  --namespace "$NAMESPACE" \
  --create-namespace \
  -f "$SCRIPT_DIR/values.yaml" \
  --set "loki.limits_config.retention_period=${RETENTION_PERIOD}" \
  --wait --timeout 5m

echo "Waiting for Loki to be ready..."
kubectl rollout status statefulset/loki -n "$NAMESPACE" --timeout=120s || true

echo "Installing Promtail..."

# Clean up stuck pending releases if any
if helm status promtail -n "$NAMESPACE" 2>/dev/null | grep -q "pending-"; then
  echo "Cleaning up stuck Promtail release..."
  helm delete promtail -n "$NAMESPACE" --wait 2>/dev/null || true
fi

helm upgrade --install promtail grafana/promtail \
  --namespace "$NAMESPACE" \
  -f "$PROMTAIL_VALUES_FILE" \
  --wait --timeout 5m

echo "Waiting for Promtail to be ready..."
kubectl rollout status daemonset/promtail -n "$NAMESPACE" --timeout=120s || true

echo "Loki + Promtail installed successfully."
