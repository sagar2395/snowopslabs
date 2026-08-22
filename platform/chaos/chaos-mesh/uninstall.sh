#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="chaos-mesh"

echo "Uninstalling Chaos Mesh..."

# Remove all chaos experiments first
echo "Cleaning up active chaos experiments..."
for kind in PodChaos NetworkChaos StressChaos IOChaos TimeChaos DNSChaos HTTPChaos; do
  kubectl delete "$kind" --all --all-namespaces >/dev/null 2>&1 || true
done

# Uninstall Helm release
echo "Removing Chaos Mesh Helm release..."
helm uninstall chaos-mesh -n $NAMESPACE >/dev/null 2>&1 || true

# Clean up CRDs
echo "Removing Chaos Mesh CRDs..."
kubectl delete crd -l app.kubernetes.io/part-of=chaos-mesh >/dev/null 2>&1 || true
# `xargs -r` is GNU-only, so collect first and only delete when non-empty —
# otherwise BSD xargs would run `kubectl delete` with no arguments.
leftover_crds=$(kubectl get crd -o name 2>/dev/null | grep chaos-mesh.org || true)
if [ -n "$leftover_crds" ]; then
  # shellcheck disable=SC2086  # deliberate word splitting: one arg per CRD
  kubectl delete $leftover_crds >/dev/null 2>&1 || true
fi

# Delete namespace
echo "Removing namespace..."
kubectl delete namespace $NAMESPACE --ignore-not-found --timeout=60s >/dev/null 2>&1 || true

echo ""
echo "Chaos Mesh uninstalled successfully"
