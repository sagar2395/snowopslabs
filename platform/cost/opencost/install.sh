#!/usr/bin/env bash
set -euo pipefail

# OpenCost — real-time Kubernetes cost monitoring backed by Prometheus.
# Deploys as a standalone Deployment + service exposing a UI on port 9090
# (proxied through the cluster ingress). OpenCost reads resource usage from
# the existing Prometheus and applies on-prem pricing defaults when no cloud
# billing API is configured (suitable for k3d/lab use).
#
# Config (env, with defaults — scripts never source .env themselves):
#   OPENCOST_CHART_VERSION  pinned Helm chart version (versions.env)
#   MONITORING_NAMESPACE    namespace where Prometheus runs (default: monitoring)
#   PROMETHEUS_SVC          Prometheus service address for OpenCost to query

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="opencost"
CHART_VERSION="${OPENCOST_CHART_VERSION:-1.43.2}"
MONITORING_NS="${MONITORING_NAMESPACE:-monitoring}"
PROMETHEUS_SVC="${PROMETHEUS_SVC:-http://prometheus-kube-prometheus-prometheus.${MONITORING_NS}.svc:9090}"

echo "Installing OpenCost ${CHART_VERSION} (namespace=${NAMESPACE})..."

helm repo add opencost https://opencost.github.io/opencost-helm-chart --force-update
helm repo update opencost

helm upgrade --install opencost opencost/opencost \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$CHART_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --set "opencost.exporter.defaultClusterId=lab-cluster" \
  --set "opencost.prometheus.external.url=${PROMETHEUS_SVC}" \
  --wait --timeout 5m

echo "Waiting for OpenCost to be ready..."
kubectl rollout status deployment/opencost -n "$NAMESPACE" --timeout=120s

echo ""
echo "OpenCost installed."
echo "  UI (port-forward fallback): kubectl -n ${NAMESPACE} port-forward svc/opencost 9090 &"
echo "  If ingress is up: http://opencost.\${DOMAIN_SUFFIX:-k3d.local}"
echo "  NOTE: k3d has no real billing — OpenCost uses on-prem default pricing (~\$0.048/CPU-hr)."
