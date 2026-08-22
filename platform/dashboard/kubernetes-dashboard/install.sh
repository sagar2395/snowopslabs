#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# DOMAIN_SUFFIX and INGRESS_CLASS are provided by the executor environment.
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"
INGRESS_CLASS="${INGRESS_CLASS:-traefik}"

NAMESPACE="kubernetes-dashboard"

echo "==> Installing Kubernetes Dashboard..."

# Create namespace
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# The project moved to kubernetes-retired; both the Helm repo (404) and
# OCI registry (403) are broken. Install directly from the release tarball.
CHART_VERSION="7.14.0"
CHART_URL="https://github.com/kubernetes-retired/dashboard/releases/download/kubernetes-dashboard-${CHART_VERSION}/kubernetes-dashboard-${CHART_VERSION}.tgz"

helm upgrade --install kubernetes-dashboard "$CHART_URL" \
  --namespace "$NAMESPACE" \
  --values "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

# Apply admin user and RBAC
kubectl apply -f "$SCRIPT_DIR/admin-user.yaml"

# Create Ingress for dashboard access (HTTP — Kong TLS is disabled in values.yaml)
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kubernetes-dashboard-ingress
  namespace: $NAMESPACE
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web
spec:
  ingressClassName: traefik
  rules:
  - host: dashboard.$DOMAIN_SUFFIX
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: kubernetes-dashboard-kong-proxy
            port:
              number: 80
EOF

echo "==> Kubernetes Dashboard installed."
echo "    URL: http://dashboard.$DOMAIN_SUFFIX"
echo ""
echo "    To get an access token:"
echo "    kubectl -n $NAMESPACE create token admin-user"
