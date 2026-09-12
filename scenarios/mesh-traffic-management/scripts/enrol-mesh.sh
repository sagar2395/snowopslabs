#!/usr/bin/env bash
# Enrol the workload namespace into the mesh and restart what is already
# running so sidecars are injected.
#
# This is platform plumbing, not the lesson: without it every VirtualService,
# DestinationRule and PeerAuthentication in this scenario is silently inert —
# the CRs exist, the mesh ignores them, and nothing routes. The mesh installer
# only labels the namespace if it exists at install time, so a namespace created
# (or recreated) afterwards is never enrolled.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

NS="${WORKLOAD_NAMESPACE}"

echo "Enrolling namespace '$NS' into the mesh (istio-injection=enabled)..."
kubectl label namespace "$NS" istio-injection=enabled --overwrite

if kubectl get deployments -n "$NS" --no-headers 2>/dev/null | grep -q .; then
  echo "Restarting deployments in '$NS' so the sidecar is injected..."
  kubectl rollout restart deployment -n "$NS"
  kubectl rollout status deployment -n "$NS" --timeout=180s || true
fi

echo "Namespace '$NS' is enrolled."
