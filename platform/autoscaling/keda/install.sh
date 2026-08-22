#!/usr/bin/env bash
set -euo pipefail

# KEDA (Kubernetes Event-Driven Autoscaling) — provides ScaledObject/ScaledJob
# CRDs and an autoscaler that drives an HPA from external metrics (Prometheus,
# Kafka lag, …). Selected via AUTOSCALING_PROVIDER=keda. Portable + idempotent.
#
# Config (env, with defaults — scripts never source .env themselves):
#   KEDA_CHART_VERSION   pinned kedacore/keda chart version (versions.env)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="keda"
CHART_VERSION="${KEDA_CHART_VERSION:-2.15.1}"

echo "Installing KEDA ${CHART_VERSION} (namespace=${NAMESPACE})..."

helm repo add kedacore https://kedacore.github.io/charts --force-update
helm repo update kedacore

helm upgrade --install keda kedacore/keda \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$CHART_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

echo "Waiting for the KEDA operator + metrics server to be ready..."
kubectl rollout status deployment/keda-operator -n "$NAMESPACE" --timeout=180s
kubectl rollout status deployment/keda-operator-metrics-apiserver -n "$NAMESPACE" --timeout=180s

echo ""
echo "KEDA installed successfully."
echo "    Operator + metrics server: namespace '$NAMESPACE'"
echo "    Define autoscaling with a ScaledObject (see the autoscaling-under-load scenario)."
