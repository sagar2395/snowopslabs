#!/usr/bin/env bash
# check-memory-request.sh — the memory request must sit ABOVE observed peak
# usage and BELOW the waste ceiling. See check-cpu-request.sh for why both
# bounds exist; for memory the downside is worse, because there is no
# throttling — the kernel OOM-kills the container.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
. "$DIR/_prom.sh"

NAMESPACE="${WORKLOAD_NAMESPACE}"
APP="${WORKLOAD_NAME}"
CONTAINER="${CONTAINER:-$APP}"
MAX_MEMORY_MIB="${MAX_MEMORY_MIB:-256}"

if ! kubectl get deployment "$APP" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "FAIL: deployment ${NAMESPACE}/${APP} not found — deploy it first (labctl app deploy ${APP})." >&2
  exit 1
fi

raw=$(kubectl get deployment "$APP" -n "$NAMESPACE" \
  -o jsonpath="{.spec.template.spec.containers[?(@.name==\"$CONTAINER\")].resources.requests.memory}" \
  2>/dev/null || true)
if [ -z "$raw" ]; then
  raw=$(kubectl get deployment "$APP" -n "$NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || true)
fi

if [ -z "$raw" ]; then
  echo "FAIL: no memory request set on ${NAMESPACE}/${APP} (container '${CONTAINER}')." >&2
  echo "Set one — a pod with no memory request has no reservation to protect it:" >&2
  echo "  kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=memory=64Mi" >&2
  exit 1
fi

# "1Gi" -> 1024, "256Mi" -> 256, "512M" -> ~488, bare bytes -> MiB
to_mib() {
  case "$1" in
    *Gi) echo "${1%Gi}" | awk '{printf "%d", $1 * 1024}' ;;
    *Mi) echo "${1%Mi}" ;;
    *G) echo "${1%G}" | awk '{printf "%d", $1 * 953.674}' ;;
    *M) echo "${1%M}" | awk '{printf "%d", $1 / 1.04858}' ;;
    *Ki) echo "${1%Ki}" | awk '{printf "%d", $1 / 1024}' ;;
    *) echo "$1" | awk '{printf "%d", $1 / (1024*1024)}' ;;
  esac
}
actual_mib=$(to_mib "$raw")

if [ "$actual_mib" -gt "$MAX_MEMORY_MIB" ]; then
  echo "FAIL: memory request ${raw} (${actual_mib}Mi) is over the waste ceiling (<= ${MAX_MEMORY_MIB}Mi)." >&2
  echo "Read the peak, then size just above it:" >&2
  echo "  kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=memory=<peak+headroom>" >&2
  exit 1
fi

peak_bytes="$(prom_scalar "max_over_time((max(container_memory_working_set_bytes{namespace=\"${NAMESPACE}\",container=\"${CONTAINER}\"}) or vector(0))[${USAGE_WINDOW}:1m])")"
peak_mib="$(echo "${peak_bytes:-0}" | awk '{printf "%d", $1 / (1024*1024)}')"

if [ -z "$peak_bytes" ]; then
  echo "FAIL: Prometheus returned no memory usage for ${NAMESPACE}/${CONTAINER}, so the request" >&2
  echo "cannot be checked against what the workload actually needs." >&2
  echo "  labctl traffic start --app ${WORKLOAD_NAME} --profile steady --rps 25" >&2
  exit 1
fi

if [ "$actual_mib" -lt "$peak_mib" ]; then
  echo "FAIL: memory request ${raw} (${actual_mib}Mi) is BELOW the observed peak over ${USAGE_WINDOW} (${peak_mib}Mi)." >&2
  echo "A memory request under real usage is not throttled the way CPU is — the kernel" >&2
  echo "OOM-kills the container. Size above the peak:" >&2
  echo "  kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=memory=$((peak_mib + 16))Mi" >&2
  exit 1
fi

echo "OK: memory request ${raw} (${actual_mib}Mi) sits above the ${USAGE_WINDOW} peak (${peak_mib}Mi) and under the ${MAX_MEMORY_MIB}Mi ceiling."
