#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=../../_lib/helm.sh
. "$(cd "$SCRIPT_DIR/../.." && pwd)/_lib/helm.sh"
PROMTAIL_VALUES_FILE="$(mktemp "${TMPDIR:-/tmp}/promtail-values.XXXXXX")"
trap 'rm -f "$PROMTAIL_VALUES_FILE"' EXIT

# Log retention: default 7 days (168h). Set LOKI_RETENTION_HOURS to override.
# Set to 0 to disable retention (logs never expire).
RETENTION_HOURS="${LOKI_RETENTION_HOURS:-168}"
GRAFANA_CHARTS_REPO="${GRAFANA_CHARTS_REPO:-https://grafana-community.github.io/helm-charts}"
PROMTAIL_CHARTS_REPO="${PROMTAIL_CHARTS_REPO:-https://grafana.github.io/helm-charts}"
LOKI_CHART_VERSION="${LOKI_CHART_VERSION:-18.12.0}"
PROMTAIL_CHART_VERSION="${PROMTAIL_CHART_VERSION:-6.17.1}"
if [ "$RETENTION_HOURS" = "0" ]; then
  RETENTION_PERIOD="0s"
else
  RETENTION_PERIOD="${RETENTION_HOURS}h"
fi

echo "Installing Loki (single-binary mode, namespace=${NAMESPACE}, retention=${RETENTION_PERIOD})..."

# Loki's chart is maintained in grafana-community; Promtail only exists in the
# original grafana repo (it is deprecated and has no successor there).
helm repo add grafana-community "$GRAFANA_CHARTS_REPO" --force-update
helm repo add grafana "$PROMTAIL_CHARTS_REPO" --force-update
helm repo update grafana-community grafana

# Clean up stuck pending releases if any
if helm status loki -n "$NAMESPACE" 2>/dev/null | grep -q "pending-"; then
  echo "Cleaning up stuck Loki release..."
  helm delete loki -n "$NAMESPACE" --wait 2>/dev/null || true
fi

# Install with runtime retention override. Using --set (not sed -i) keeps the
# script portable across macOS and Linux (avoids GNU-only sed -i behaviour).
helm_upgrade_install loki "$NAMESPACE" grafana-community/loki \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$LOKI_CHART_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --set "loki.limits_config.retention_period=${RETENTION_PERIOD}" \
  --wait --timeout 5m

echo "Waiting for Loki to be ready..."
kubectl rollout status statefulset/loki -n "$NAMESPACE" --timeout=120s || true

echo "Installing Promtail..."

# Render promtail-values.yaml with the real monitoring namespace. This file used
# to be passed as an empty mktemp, so Promtail silently ran on chart defaults
# and none of the settings below took effect.
sed "s/\.monitoring\.svc/.${NAMESPACE}.svc/g" "$SCRIPT_DIR/promtail-values.yaml" >"$PROMTAIL_VALUES_FILE"

# Clean up stuck pending releases if any
if helm status promtail -n "$NAMESPACE" 2>/dev/null | grep -q "pending-"; then
  echo "Cleaning up stuck Promtail release..."
  helm delete promtail -n "$NAMESPACE" --wait 2>/dev/null || true
fi

helm_upgrade_install promtail "$NAMESPACE" grafana/promtail \
  --namespace "$NAMESPACE" \
  --version "$PROMTAIL_CHART_VERSION" \
  -f "$PROMTAIL_VALUES_FILE" \
  --wait --timeout 5m

echo "Waiting for Promtail to be ready..."
kubectl rollout status daemonset/promtail -n "$NAMESPACE" --timeout=120s || true

echo "Loki + Promtail installed successfully."
