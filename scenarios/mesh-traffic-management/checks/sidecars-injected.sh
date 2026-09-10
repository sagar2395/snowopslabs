#!/usr/bin/env bash
# Every canary pod must carry an istio-proxy container.
#
# This is a guard, not a lesson. Without a sidecar the mesh sees none of this
# workload's traffic: the VirtualService routes nothing, the PeerAuthentication
# enforces nothing, and every CR-existence assertion still passes. Grading that
# state as success is the failure mode this check exists to make impossible.
set -euo pipefail

NS="${WORKLOAD_NAMESPACE:-go-api}"
APP="${WORKLOAD_NAME:-go-api}"

PODS="$(kubectl -n "$NS" get pods -l "app=${APP}-canary" \
  --field-selector=status.phase=Running \
  -o 'jsonpath={range .items[*]}{.metadata.name}{"="}{.spec.containers[*].name}{"\n"}{end}' 2>/dev/null || true)"

if [ -z "$PODS" ]; then
  echo "FAIL: no Running canary pods found in $NS (label app=${APP}-canary)." >&2
  exit 1
fi

MISSING=""
COUNT=0
while IFS= read -r line; do
  [ -n "$line" ] || continue
  COUNT=$((COUNT + 1))
  case "$line" in
    *istio-proxy*) ;;
    *) MISSING="$MISSING ${line%%=*}" ;;
  esac
done <<EOF2
$PODS
EOF2

if [ -n "$MISSING" ]; then
  echo "FAIL: these canary pods have no istio-proxy sidecar:$MISSING" >&2
  echo "  The mesh cannot see their traffic, so routing and mTLS are inert." >&2
  echo "  The namespace is most likely not enrolled. Check and re-run the stage:" >&2
  echo "    kubectl get ns $NS --show-labels" >&2
  echo "    kubectl label namespace $NS istio-injection=enabled --overwrite" >&2
  echo "    kubectl -n $NS rollout restart deployment" >&2
  exit 1
fi

echo "OK: all $COUNT Running canary pod(s) carry an istio-proxy sidecar."
