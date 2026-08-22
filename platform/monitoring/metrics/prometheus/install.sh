#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"

echo "Installing Prometheus Stack (prometheus-operator, kube-prometheus-stack, node-exporter, kube-state-metrics)..."

# Add Helm repo and update
echo "Adding Prometheus Helm repository..."
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
helm repo update

# Install Prometheus Stack
echo "Installing kube-prometheus-stack chart..."
HELM_ARGS=(
  upgrade --install prometheus prometheus-community/kube-prometheus-stack
  --namespace "$NAMESPACE"
  --create-namespace
  -f "$(dirname "$0")/values.yaml"
  --wait --timeout 5m
)
# Route lab-fault pages to an external webhook (Slack bridge, Teams, etc.)
# instead of the in-cluster pager when ALERT_WEBHOOK_URL is set.
# Index [1] is the labfault-pager receiver declared in values.yaml.
if [ -n "${ALERT_WEBHOOK_URL:-}" ]; then
  echo "Routing lab-fault alerts to ${ALERT_WEBHOOK_URL}"
  HELM_ARGS+=(--set-string "alertmanager.config.receivers[1].webhook_configs[0].url=${ALERT_WEBHOOK_URL}")
fi
helm "${HELM_ARGS[@]}"

# Wait for Prometheus to be ready (StatefulSet, not Deployment)
echo "Waiting for Prometheus to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=prometheus -n "$NAMESPACE" --timeout=120s 2>/dev/null || true

# Create Prometheus Ingress with dynamic domain
echo "Creating Prometheus ingress for ${DOMAIN_SUFFIX}..."
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: prometheus
  namespace: $NAMESPACE
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web
spec:
  ingressClassName: traefik
  rules:
    - host: prometheus.${DOMAIN_SUFFIX}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: prometheus-kube-prometheus-prometheus
                port:
                  number: 9090
EOF

# Alertmanager ingress: labctl incident status queries the Alertmanager API
# from the host to report whether a fault's expected alert fired.
echo "Creating Alertmanager ingress for ${DOMAIN_SUFFIX}..."
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: alertmanager
  namespace: $NAMESPACE
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web
spec:
  ingressClassName: traefik
  rules:
    - host: alertmanager.${DOMAIN_SUFFIX}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: prometheus-kube-prometheus-alertmanager
                port:
                  number: 9093
EOF

echo "Prometheus Stack installed successfully"
echo ""
echo "Access Prometheus at:   http://prometheus.${DOMAIN_SUFFIX} (via Traefik ingress)"
echo "Access Alertmanager at: http://alertmanager.${DOMAIN_SUFFIX} (via Traefik ingress)"
echo "Namespace: $NAMESPACE"
