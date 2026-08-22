#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="cert-manager"
SCRIPT_DIR="$(dirname "$0")"

echo "Uninstalling cert-manager..."

# Remove ClusterIssuer first
kubectl delete -f "$SCRIPT_DIR/cluster-issuer.yaml" --ignore-not-found >/dev/null 2>&1 || true

# Uninstall cert-manager
helm uninstall cert-manager -n $NAMESPACE >/dev/null 2>&1 || true

# Remove CRDs
echo "Removing cert-manager CRDs..."
kubectl delete crd certificaterequests.cert-manager.io certificates.cert-manager.io \
  challenges.acme.cert-manager.io clusterissuers.cert-manager.io issuers.cert-manager.io \
  orders.acme.cert-manager.io >/dev/null 2>&1 || true

# Delete namespace
kubectl delete namespace $NAMESPACE --ignore-not-found --timeout=60s >/dev/null 2>&1 || true

echo "cert-manager uninstalled successfully"
