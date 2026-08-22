#!/usr/bin/env bash
set -euo pipefail

# check-cpu-request.sh — exit 0 only if go-api's CPU request is <= 100m.
#
# kubectl returns Kubernetes quantity strings like "50m", "100m", "4000m", "4"
# (= 4000m). This script converts them to millicores for numeric comparison.
# Target: <= 100m (the production-realistic baseline for this API at low traffic).

NAMESPACE="${NAMESPACE:-go-api}"
APP="${APP:-go-api}"
MAX_CPU_MILLICORES=100 # threshold in millicores

raw=$(kubectl get deployment "$APP" -n "$NAMESPACE" \
  -o jsonpath='{.spec.template.spec.containers[0].resources.requests.cpu}' \
  2>/dev/null || true)

if [ -z "$raw" ]; then
  echo "FAIL: could not read CPU request from ${NAMESPACE}/${APP}. Is the deployment running?" >&2
  exit 1
fi

# Convert to millicores: "4000m" -> 4000, "4" -> 4000, "0.5" -> 500, "50m" -> 50
to_millicores() {
  local q="$1"
  case "$q" in
    *m) echo "${q%m}" ;;
    *) echo "$q" | awk '{printf "%d", $1 * 1000}' ;;
  esac
}

actual_mc=$(to_millicores "$raw")

if [ "$actual_mc" -le "$MAX_CPU_MILLICORES" ]; then
  echo "OK: CPU request ${raw} (${actual_mc}m) is within the right-sized threshold (<= ${MAX_CPU_MILLICORES}m)."
else
  echo "FAIL: CPU request ${raw} (${actual_mc}m) is over the right-sized threshold (<= ${MAX_CPU_MILLICORES}m)." >&2
  echo "Right-size with: kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=cpu=50m,memory=32Mi" >&2
  exit 1
fi
