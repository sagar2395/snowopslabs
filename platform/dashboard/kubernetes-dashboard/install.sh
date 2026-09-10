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
CHART_VERSION="${KUBERNETES_DASHBOARD_CHART_VERSION:-7.14.0}"
CHART_URL="https://github.com/kubernetes-retired/dashboard/releases/download/kubernetes-dashboard-${CHART_VERSION}/kubernetes-dashboard-${CHART_VERSION}.tgz"

helm upgrade --install kubernetes-dashboard "$CHART_URL" \
  --namespace "$NAMESPACE" \
  --values "$SCRIPT_DIR/values.yaml" \
  --wait --timeout 5m

# Apply admin user and RBAC
kubectl apply -f "$SCRIPT_DIR/admin-user.yaml"

# The Ingress backend has to exist before the Ingress does. A dangling backend
# is not a local failure: traefik retries the missing Service in a hot loop
# until it fails its own liveness probe, which takes every other ingress in the
# lab down with it. Discover the proxy Service and refuse to create an Ingress
# without one.
# The trailing '|| true' is load-bearing: under 'set -e' with pipefail, grep
# finding nothing would abort the script here, before the message below that
# explains what is wrong.
PROXY_SVC="$(kubectl -n "$NAMESPACE" get svc -o name 2>/dev/null \
  | cut -d/ -f2 | grep -- '-kong-proxy$' | head -1 || true)"

if [ -z "$PROXY_SVC" ]; then
  echo "ERROR: the dashboard's Kong proxy Service was not created." >&2
  echo "  Kong renders a proxy Service only when a listener is enabled, and the" >&2
  echo "  chart ships proxy.http.enabled=false. Check kong.proxy in values.yaml." >&2
  echo "  No Ingress was created: a dangling one would destabilise traefik." >&2
  exit 1
fi

PROXY_PORT="$(kubectl -n "$NAMESPACE" get svc "$PROXY_SVC" \
  -o jsonpath='{.spec.ports[0].port}')"

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
  ingressClassName: $INGRESS_CLASS
  rules:
  - host: dashboard.$DOMAIN_SUFFIX
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: $PROXY_SVC
            port:
              number: $PROXY_PORT
EOF

echo "==> Kubernetes Dashboard installed."
echo "    URL: http://dashboard.$DOMAIN_SUFFIX"
echo ""
echo "    To get an access token:"
echo "    kubectl -n $NAMESPACE create token admin-user"
