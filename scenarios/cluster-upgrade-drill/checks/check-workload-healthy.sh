#!/usr/bin/env bash
set -euo pipefail

APP_NAMESPACE="${APP_NAMESPACE:-${WORKLOAD_NAMESPACE:-go-api}}"

PODS="$(kubectl -n "$APP_NAMESPACE" get pods -l app=${WORKLOAD_NAME:-go-api} \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.phase}{" "}{.spec.nodeName}{"\n"}{end}' \
  2>/dev/null || true)"

if [ -z "$PODS" ]; then
  echo "FAIL: no ${WORKLOAD_NAME:-go-api} pods found in namespace ${APP_NAMESPACE}." >&2
  exit 1
fi

# Surface image-pull failures explicitly — the generic "not Running" message
# sends a learner looking in the wrong place.
STUCK="$(kubectl -n "$APP_NAMESPACE" get pods -l app=${WORKLOAD_NAME:-go-api} \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{range .status.containerStatuses[*]}{.state.waiting.reason}{end}{"\n"}{end}' \
  2>/dev/null | grep -E 'ImagePullBackOff|ErrImagePull' || true)"
if [ -n "$STUCK" ]; then
  echo "FAIL: ${WORKLOAD_NAME:-go-api} pod(s) cannot pull their image:" >&2
  echo "$STUCK" >&2
  echo "The replacement node has an empty image store. Re-import the lab images:" >&2
  echo "  k3d image import ${WORKLOAD_NAME:-go-api}:v1.2.0 -c \${CLUSTER_NAME:-snowops}" >&2
  exit 1
fi

NOT_RUNNING="$(echo "$PODS" | awk '$2 != "Running" {print $1" ("$2")"}')"
if [ -n "$NOT_RUNNING" ]; then
  echo "FAIL: ${WORKLOAD_NAME:-go-api} pod(s) are not Running:" >&2
  echo "$NOT_RUNNING" >&2
  exit 1
fi

NODES="$(echo "$PODS" | awk '{print $3}' | sort -u | grep -c . || true)"
if [ "${NODES:-0}" -lt 2 ]; then
  echo "FAIL: all ${WORKLOAD_NAME:-go-api} pods are on a single node (${NODES} node(s) in use)." >&2
  echo "A rolling upgrade must leave the workload spread across nodes; one node" >&2
  echo "holding every replica means the next drain takes the whole app down." >&2
  kubectl -n "$APP_NAMESPACE" get pods -l app=${WORKLOAD_NAME:-go-api} -o wide >&2
  exit 1
fi

echo "OK: ${WORKLOAD_NAME:-go-api} is Running across ${NODES} nodes."
