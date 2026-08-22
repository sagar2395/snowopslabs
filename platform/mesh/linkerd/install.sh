#!/usr/bin/env bash
set -euo pipefail

# Linkerd service mesh installer.
# Selected via MESH_PROVIDER=linkerd. Portable + idempotent.
#
# Linkerd's mTLS identity needs a trust anchor (root CA) + an issuer cert.
# We generate them with openssl (portable on macOS + Linux) instead of the
# `step` CLI so there is no extra tool to install. Certs are regenerated on a
# fresh install and reused (read back from the cluster) on re-runs so the
# control plane keeps a stable identity across idempotent installs.
#
# Config (env, with defaults — scripts never source .env themselves):
#   LINKERD_CRDS_CHART_VERSION           pinned linkerd-crds chart version
#   LINKERD_CONTROL_PLANE_CHART_VERSION  pinned linkerd-control-plane version
#   MESH_NAMESPACE                       workload namespace to enrol (default: go-api)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SYSTEM_NS="linkerd"
CRDS_VERSION="${LINKERD_CRDS_CHART_VERSION:-1.8.0}"
CP_VERSION="${LINKERD_CONTROL_PLANE_CHART_VERSION:-1.16.11}"
MESH_NAMESPACE="${MESH_NAMESPACE:-go-api}"

command -v openssl >/dev/null 2>&1 || {
  echo "openssl is required for Linkerd cert generation" >&2
  exit 1
}

CERT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/linkerd-certs.XXXXXX")"
trap 'rm -rf "$CERT_DIR"' EXIT

echo "Installing Linkerd (crds=${CRDS_VERSION}, control-plane=${CP_VERSION})..."

helm repo add linkerd https://helm.linkerd.io/stable --force-update
helm repo update linkerd

# --- mTLS identity certificates ------------------------------------------------
# Reuse the existing trust anchor + issuer if the control plane is already
# installed; otherwise generate a fresh ECDSA P-256 hierarchy.
if helm status linkerd-control-plane -n "$SYSTEM_NS" >/dev/null 2>&1 &&
  kubectl get secret linkerd-identity-issuer -n "$SYSTEM_NS" >/dev/null 2>&1; then
  echo "Reusing existing Linkerd identity certificates..."
  # kubectl decodes (base64decode template fn) so we don't depend on a host
  # base64 whose decode flag differs across macOS (-D) and Linux (-d).
  kubectl get cm linkerd-identity-trust-roots -n "$SYSTEM_NS" \
    -o jsonpath='{.data.ca-bundle\.crt}' >"$CERT_DIR/ca.crt"
  kubectl get secret linkerd-identity-issuer -n "$SYSTEM_NS" \
    -o go-template='{{index .data "tls.crt" | base64decode}}' >"$CERT_DIR/issuer.crt"
  kubectl get secret linkerd-identity-issuer -n "$SYSTEM_NS" \
    -o go-template='{{index .data "tls.key" | base64decode}}' >"$CERT_DIR/issuer.key"
else
  echo "Generating Linkerd trust anchor + issuer (ECDSA P-256)..."
  # Trust anchor (self-signed root CA).
  openssl ecparam -name prime256v1 -genkey -noout -out "$CERT_DIR/ca.key"
  openssl req -x509 -new -key "$CERT_DIR/ca.key" -sha256 -days 3650 \
    -out "$CERT_DIR/ca.crt" \
    -subj "/CN=root.linkerd.cluster.local" \
    -addext "keyUsage=critical,digitalSignature,keyCertSign" \
    -addext "basicConstraints=critical,CA:TRUE"

  # Identity issuer (intermediate CA), signed by the trust anchor.
  openssl ecparam -name prime256v1 -genkey -noout -out "$CERT_DIR/issuer.key"
  openssl req -new -key "$CERT_DIR/issuer.key" -sha256 \
    -out "$CERT_DIR/issuer.csr" \
    -subj "/CN=identity.linkerd.cluster.local"
  openssl x509 -req -in "$CERT_DIR/issuer.csr" -sha256 -days 3650 \
    -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
    -out "$CERT_DIR/issuer.crt" \
    -extfile <(printf "keyUsage=critical,digitalSignature,keyCertSign\nbasicConstraints=critical,CA:TRUE\nsubjectAltName=DNS:identity.linkerd.cluster.local\n")
fi

# --- CRDs ---------------------------------------------------------------------
helm upgrade --install linkerd-crds linkerd/linkerd-crds \
  --namespace "$SYSTEM_NS" \
  --create-namespace \
  --version "$CRDS_VERSION" \
  --wait --timeout 5m

# --- Control plane ------------------------------------------------------------
helm upgrade --install linkerd-control-plane linkerd/linkerd-control-plane \
  --namespace "$SYSTEM_NS" \
  --version "$CP_VERSION" \
  -f "$SCRIPT_DIR/values.yaml" \
  --set-file identityTrustAnchorsPEM="$CERT_DIR/ca.crt" \
  --set-file identity.issuer.tls.crtPEM="$CERT_DIR/issuer.crt" \
  --set-file identity.issuer.tls.keyPEM="$CERT_DIR/issuer.key" \
  --wait --timeout 5m

echo "Waiting for Linkerd control plane to be ready..."
kubectl rollout status deployment -n "$SYSTEM_NS" --timeout=180s || true

# --- Enrol the workload namespace --------------------------------------------
if kubectl get namespace "$MESH_NAMESPACE" >/dev/null 2>&1; then
  echo "Enrolling namespace '$MESH_NAMESPACE' into the mesh (linkerd.io/inject=enabled)..."
  kubectl annotate namespace "$MESH_NAMESPACE" linkerd.io/inject=enabled --overwrite
  if kubectl get deployments -n "$MESH_NAMESPACE" --no-headers 2>/dev/null | grep -q .; then
    echo "Restarting deployments in '$MESH_NAMESPACE' to inject proxies..."
    kubectl rollout restart deployment -n "$MESH_NAMESPACE"
    kubectl rollout status deployment -n "$MESH_NAMESPACE" --timeout=120s || true
  fi
else
  echo "Namespace '$MESH_NAMESPACE' not found yet."
  echo "Create it and deploy your app, then re-run to inject proxies:"
  echo "    kubectl create namespace $MESH_NAMESPACE"
  echo "    kubectl annotate namespace $MESH_NAMESPACE linkerd.io/inject=enabled"
fi

echo ""
echo "Linkerd installed successfully."
echo "    Control plane: namespace '$SYSTEM_NS'"
echo "    Meshed namespace: '$MESH_NAMESPACE' (annotation linkerd.io/inject=enabled)"
echo "    Verify a proxy: kubectl get pod -n $MESH_NAMESPACE -o jsonpath='{.items[0].spec.containers[*].name}'"
