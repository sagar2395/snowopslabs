#!/usr/bin/env bash
set -euo pipefail

# Remove KEDA and its CRDs. Idempotent.
# Deleting the CRDs also removes any ScaledObjects/ScaledJobs (which in turn
# removes the HPAs KEDA generated) — a clean teardown.

NAMESPACE="keda"

echo "Uninstalling KEDA..."

if helm status keda -n "$NAMESPACE" >/dev/null 2>&1; then
  helm uninstall keda -n "$NAMESPACE"
fi

# Helm leaves CRDs behind; remove them so teardown is complete.
for crd in \
  scaledobjects.keda.sh \
  scaledjobs.keda.sh \
  triggerauthentications.keda.sh \
  clustertriggerauthentications.keda.sh \
  cloudeventsources.keda.sh \
  clustercloudeventsources.keda.sh; do
  kubectl delete crd "$crd" --ignore-not-found 2>/dev/null || true
done

if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  kubectl delete namespace "$NAMESPACE" --timeout=60s || true
fi

echo ""
echo "KEDA uninstalled."
