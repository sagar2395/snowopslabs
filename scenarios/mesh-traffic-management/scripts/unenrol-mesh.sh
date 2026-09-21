#!/usr/bin/env bash
# Reverses enrol-mesh.sh: drop the injection label and restart so the workload
# leaves the mesh with no sidecar, exactly as `scenario up` found it.
#
# It also removes the routing the learner applied by hand. The engine only
# tracks what it staged, so without this the VirtualService and the STRICT
# PeerAuthentication survive teardown — and the next run starts with the split
# already in place and its checks green before the learner has done anything.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

NS="${WORKLOAD_NAMESPACE}"
APP="${WORKLOAD_NAME}"

echo "Removing learner-applied mesh policy in '$NS'..."
kubectl -n "$NS" delete virtualservice "${APP}-canary" --ignore-not-found >/dev/null 2>&1 || true
kubectl -n "$NS" delete peerauthentication "${APP}-mtls" --ignore-not-found >/dev/null 2>&1 || true

echo "Un-enrolling namespace '$NS' from the mesh..."
kubectl label namespace "$NS" istio-injection- --overwrite 2>/dev/null || true

if kubectl get deployments -n "$NS" --no-headers 2>/dev/null | grep -q .; then
  kubectl rollout restart deployment -n "$NS" 2>/dev/null || true
  kubectl rollout status deployment -n "$NS" --timeout=180s || true
fi

echo "Namespace '$NS' is no longer meshed."
