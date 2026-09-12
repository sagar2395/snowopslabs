#!/usr/bin/env bash
# check-cpu-request.sh — the CPU request must sit ABOVE observed peak usage and
# BELOW the waste ceiling.
#
# Both bounds are the point. An upper bound alone makes `--requests=cpu=1m` a
# full-marks answer to a scenario about sizing requests correctly, and
# under-provisioning is how workloads get throttled and evicted in production.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
. "$DIR/_prom.sh"

NAMESPACE="${WORKLOAD_NAMESPACE}"
APP="${WORKLOAD_NAME}"
CONTAINER="${CONTAINER:-$APP}"
MAX_CPU_MILLICORES="${MAX_CPU_MILLICORES:-100}"

if ! kubectl get deployment "$APP" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "FAIL: deployment ${NAMESPACE}/${APP} not found — deploy it first (labctl app deploy ${APP})." >&2
  exit 1
fi

raw=$(kubectl get deployment "$APP" -n "$NAMESPACE" \
  -o jsonpath="{.spec.template.spec.containers[?(@.name==\"$CONTAINER\")].resources.requests.cpu}" \
  2>/dev/null || true)
if [ -z "$raw" ]; then
  raw=$(kubectl get deployment "$APP" -n "$NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[0].resources.requests.cpu}' 2>/dev/null || true)
fi

if [ -z "$raw" ]; then
  echo "FAIL: no CPU request set on ${NAMESPACE}/${APP} (container '${CONTAINER}')." >&2
  echo "A pod with no CPU request is scheduled with no reservation at all, which is not" >&2
  echo "right-sizing — it is opting out of scheduling guarantees. Set one:" >&2
  echo "  kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=cpu=50m" >&2
  exit 1
fi

# "4000m" -> 4000, "4" -> 4000, "0.5" -> 500
to_millicores() {
  case "$1" in
    *m) echo "${1%m}" ;;
    *) echo "$1" | awk '{printf "%d", $1 * 1000}' ;;
  esac
}
actual_mc=$(to_millicores "$raw")

if [ "$actual_mc" -gt "$MAX_CPU_MILLICORES" ]; then
  echo "FAIL: CPU request ${raw} (${actual_mc}m) is over the waste ceiling (<= ${MAX_CPU_MILLICORES}m)." >&2
  echo "Requests are what the scheduler reserves and what a cloud bill is computed from," >&2
  echo "whether or not the workload uses them. Read the peak, then size just above it:" >&2
  echo "  kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=cpu=<peak+headroom>" >&2
  exit 1
fi

# The floor: peak usage over the load window, from Prometheus.
peak_cores="$(prom_scalar "max_over_time((sum(rate(container_cpu_usage_seconds_total{namespace=\"${NAMESPACE}\",container=\"${CONTAINER}\"}[1m])) or vector(0))[${USAGE_WINDOW}:1m])")"
peak_mc="$(echo "${peak_cores:-0}" | awk '{printf "%d", $1 * 1000}')"

if [ -z "$peak_cores" ]; then
  echo "FAIL: Prometheus returned no CPU usage for ${NAMESPACE}/${CONTAINER}, so the request" >&2
  echo "cannot be checked against what the workload actually needs." >&2
  echo "Right-sizing without a measurement is guessing. Drive load and try again:" >&2
  echo "  labctl traffic start --app ${WORKLOAD_NAME} --profile steady --rps 25" >&2
  exit 1
fi

if [ "$actual_mc" -lt "$peak_mc" ]; then
  echo "FAIL: CPU request ${raw} (${actual_mc}m) is BELOW the observed peak over ${USAGE_WINDOW} (${peak_mc}m)." >&2
  echo "Right-sizing is not 'make the number small'. A request under real usage means the" >&2
  echo "scheduler under-reserves, the container is throttled, and the pod is first in line" >&2
  echo "for eviction under node pressure. Size above the peak, not above the average:" >&2
  echo "  kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=cpu=$((peak_mc + 20))m" >&2
  exit 1
fi

echo "OK: CPU request ${raw} (${actual_mc}m) sits above the ${USAGE_WINDOW} peak (${peak_mc}m) and under the ${MAX_CPU_MILLICORES}m ceiling."
