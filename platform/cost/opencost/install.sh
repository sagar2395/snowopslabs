#!/usr/bin/env bash
set -euo pipefail

# OpenCost — real-time Kubernetes cost monitoring backed by Prometheus.
# Deploys as a standalone Deployment + service exposing a UI on port 9090
# (proxied through the cluster ingress). OpenCost reads resource usage from
# the existing Prometheus and applies on-prem pricing defaults when no cloud
# billing API is configured (suitable for k3d/lab use).
#
# Config (env, with defaults — scripts never source .env themselves):
#   OPENCOST_CHART_VERSION  pinned Helm chart version (config/versions.env)
#   MONITORING_NAMESPACE    namespace where Prometheus runs (default: monitoring)
#   PROMETHEUS_SVC          Prometheus service address for OpenCost to query

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="opencost"
CHART_VERSION="${OPENCOST_CHART_VERSION:-1.43.2}"
MONITORING_NS="${MONITORING_NAMESPACE:-monitoring}"
PROMETHEUS_SVC="${PROMETHEUS_SVC:-http://prometheus-kube-prometheus-prometheus.${MONITORING_NS}.svc:9090}"
# DOMAIN_SUFFIX is provided by the executor environment (from .env + runtime.env);
# it drives the OpenCost UI ingress host (opencost.<DOMAIN_SUFFIX>).
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"
INGRESS_HOST="opencost.${DOMAIN_SUFFIX}"

echo "Installing OpenCost ${CHART_VERSION} (namespace=${NAMESPACE})..."

helm repo add opencost https://opencost.github.io/opencost-helm-chart --force-update
helm repo update opencost

# Point OpenCost at the in-cluster Prometheus. Setting external.url ALONE is not
# enough: the opencost chart only honours external.url when external.enabled=true
# (otherwise it builds PROMETHEUS_SERVER_ENDPOINT from its internal defaults —
# prometheus-server.prometheus-system — which does not exist in this lab and
# leaves OpenCost unable to compute any allocation). Enable external and disable
# internal so the endpoint we pass is the one the pod actually queries.
helm upgrade --install opencost opencost/opencost \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$CHART_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --set "opencost.exporter.defaultClusterId=lab-cluster" \
  --set "opencost.prometheus.internal.enabled=false" \
  --set "opencost.prometheus.external.enabled=true" \
  --set "opencost.prometheus.external.url=${PROMETHEUS_SVC}" \
  --set "opencost.ui.ingress.enabled=true" \
  --set "opencost.ui.ingress.hosts[0].host=${INGRESS_HOST}" \
  --set "opencost.ui.ingress.hosts[0].paths[0]=/" \
  --wait --timeout 5m

echo "Waiting for OpenCost to be ready..."
kubectl rollout status deployment/opencost -n "$NAMESPACE" --timeout=120s

echo ""
echo "OpenCost installed."
echo "  UI (ingress): http://${INGRESS_HOST}"
echo "    Add the DNS entry once with: sudo labctl hosts add   (edits /etc/hosts)"
echo "  UI (port-forward fallback, if the hosts entry isn't added):"
echo "    kubectl -n ${NAMESPACE} port-forward svc/opencost 9090 &   then open http://localhost:9090"
echo "  NOTE: k3d has no real billing — OpenCost uses on-prem default pricing (~\$0.048/CPU-hr)."
