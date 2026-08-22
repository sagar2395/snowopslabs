#!/usr/bin/env bash
set -euo pipefail

# HashiCorp Vault in dev mode — fine for simulation (in-memory, auto-unsealed).
# Seeds a demo KV secret for go-api and exposes the UI via ingress.
# Portable + idempotent.
#
# SECURITY: the dev root token is read from the environment and never committed.
# Dev mode stores everything in memory and is wiped on pod restart — do NOT use
# this profile for anything real. See values.yaml for a non-dev profile sketch.
#
# Config (env, with defaults — scripts never source .env themselves):
#   VAULT_CHART_VERSION   pinned hashicorp/vault chart version (versions.env)
#   VAULT_DEV_ROOT_TOKEN  dev root token (default: root — the well-known dev default)
#   VAULT_DEMO_SECRET     demo KV value seeded at secret/go-api (default: demo value)
#   INGRESS_CLASS         ingress class for the UI route (default: traefik)
#   DOMAIN_SUFFIX         host suffix for the UI route (default: k3d.local)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="vault"
CHART_VERSION="${VAULT_CHART_VERSION:-0.28.1}"
ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-root}"
DEMO_SECRET="${VAULT_DEMO_SECRET:-s3cr3t-from-vault-v1}"
INGRESS_CLASS="${INGRESS_CLASS:-traefik}"
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"

echo "Installing HashiCorp Vault ${CHART_VERSION} (dev mode, namespace=${NAMESPACE})..."

helm repo add hashicorp https://helm.releases.hashicorp.com --force-update
helm repo update hashicorp

# Dev mode: single in-memory pod, auto-unsealed, root token from env (not logged).
helm upgrade --install vault hashicorp/vault \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --version "$CHART_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --set "server.dev.enabled=true" \
  --set "server.dev.devRootToken=${ROOT_TOKEN}" \
  --wait --timeout 5m

echo "Waiting for Vault to be ready..."
kubectl wait --for=condition=Ready pod/vault-0 -n "$NAMESPACE" --timeout=180s

# Seed a demo KV secret for go-api (kv-v2 is mounted at secret/ in dev mode).
echo "Seeding demo secret at secret/go-api ..."
kubectl exec -n "$NAMESPACE" vault-0 -- sh -c \
  "VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN='${ROOT_TOKEN}' vault kv put secret/go-api api-key='${DEMO_SECRET}'" >/dev/null

# Expose the Vault UI via ingress (served on the main vault service, port 8200).
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: vault-ui
  namespace: ${NAMESPACE}
spec:
  ingressClassName: ${INGRESS_CLASS}
  rules:
    - host: vault.${DOMAIN_SUFFIX}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: vault
                port:
                  number: 8200
EOF

echo ""
echo "Vault installed successfully (dev mode)."
echo "    UI:   http://vault.${DOMAIN_SUFFIX}  (token auth — use your dev root token)"
echo "    Addr (in-cluster): http://vault.${NAMESPACE}.svc:8200"
echo "    Demo secret: secret/go-api (key: api-key)"
echo "    Next: install external-secrets to sync it into the go-api namespace."
