#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../../_lib/helm.sh
. "$(cd "$(dirname "$0")/../.." && pwd)/_lib/helm.sh"

TRAEFIK_CHART_VERSION="${TRAEFIK_CHART_VERSION:-41.4.0}"

NAMESPACE=traefik

echo "Installing Traefik..."

# k3d ships Traefik in kube-system by default. The cluster-scoped IngressClass
# is owned by that release, which blocks our install into a separate namespace.
# Clean it up so our Helm release can manage it.
if kubectl get ingressclass traefik -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-namespace}' 2>/dev/null | grep -qv "^${NAMESPACE}$"; then
  echo "Removing k3d-bundled Traefik IngressClass (owned by another namespace)..."
  kubectl delete ingressclass traefik --ignore-not-found
fi

# Remove the k3d-bundled Traefik deployment in kube-system if present
if kubectl get deployment traefik -n kube-system &>/dev/null; then
  echo "Removing k3d-bundled Traefik from kube-system..."
  # Delete the k3s HelmChart CRD first to stop k3s from reconciling it back
  kubectl delete helmchart traefik -n kube-system --ignore-not-found 2>/dev/null || true
  kubectl delete helmchartconfig traefik -n kube-system --ignore-not-found 2>/dev/null || true
  kubectl delete deployment traefik -n kube-system --ignore-not-found
  kubectl delete service traefik -n kube-system --ignore-not-found
fi

# kind has no LoadBalancer implementation, so the Service never gets an address
# and helm --wait cannot finish. Traefik binds host ports 80/443 on the node
# that runtimes/kind/up.sh maps them to, and gives the port up before a new pod.
EXPOSE_ARGS=()
if [ "${PROFILE:-k3d}" = "kind" ]; then
  EXPOSE_ARGS=(
    --set service.spec.type=ClusterIP
    --set ports.web.hostPort=80
    --set ports.websecure.hostPort=443
    --set-string nodeSelector.ingress-ready=true
    --set "tolerations[0].key=node-role.kubernetes.io/control-plane"
    --set "tolerations[0].operator=Exists"
    --set "tolerations[0].effect=NoSchedule"
    --set updateStrategy.rollingUpdate.maxUnavailable=1
    --set updateStrategy.rollingUpdate.maxSurge=0
  )
fi

helm repo add traefik https://traefik.github.io/charts --force-update
helm repo update

# use a values file for configurability; default values live in this repo
helm_upgrade_install traefik "$NAMESPACE" traefik/traefik \
  --version "$TRAEFIK_CHART_VERSION" \
  --namespace $NAMESPACE \
  --create-namespace \
  -f "$(dirname "$0")/values.yaml" \
  ${EXPOSE_ARGS[@]+"${EXPOSE_ARGS[@]}"} \
  --set api.dashboard=true \
  --wait --timeout 5m

echo "Waiting for Traefik to be ready..."
kubectl rollout status deployment/traefik -n $NAMESPACE --timeout=120s

# Create IngressRoute for Traefik Dashboard
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-${CLUSTER_NAME:-snowops}.localhost}"
cat <<EOF | kubectl apply -f -
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata:
  name: traefik-dashboard
  namespace: $NAMESPACE
spec:
  entryPoints:
    - web
  routes:
    - match: Host(\`traefik.${DOMAIN_SUFFIX}\`)
      kind: Rule
      services:
        - name: api@internal
          kind: TraefikService
EOF

echo "Traefik installed successfully."
echo "    Dashboard: http://traefik.${INGRESS_URL_SUFFIX:-${DOMAIN_SUFFIX:-${CLUSTER_NAME:-snowops}.localhost}}/dashboard/"
