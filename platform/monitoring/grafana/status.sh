#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"

echo "Grafana Status:"
echo "==============="
echo ""

if ! kubectl get namespace $NAMESPACE &>/dev/null; then
  echo "✗ Namespace '$NAMESPACE' not found"
  exit 1
fi

echo "Pods in '$NAMESPACE' namespace:"
kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=grafana

echo ""
echo "Services in '$NAMESPACE' namespace:"
kubectl get svc -n $NAMESPACE -l app.kubernetes.io/name=grafana

echo ""
echo "Grafana Ingress:"
kubectl get ingress -n $NAMESPACE -l app.kubernetes.io/name=grafana

echo ""
echo "Grafana Access:"
echo "  - External: http://grafana.${DOMAIN_SUFFIX:-k3d.local}"
echo "  - Internal: http://grafana.$NAMESPACE.svc.cluster.local:80"
echo ""
echo "ConfigMaps:"
# `|| true`: a status command must not fail just because grep matched nothing
# (no dashboards yet). Without it, set -o pipefail makes an empty match exit 1.
kubectl get configmap -n $NAMESPACE | grep -E "grafana|dashboard" || true
