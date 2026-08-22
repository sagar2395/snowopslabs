#!/usr/bin/env bash
set -euo pipefail

# check-memory-request.sh — exit 0 only if go-api's memory request is <= 256Mi.
#
# kubectl returns Kubernetes quantity strings like "32Mi", "256Mi", "1Gi".
# This script converts them to MiB for numeric comparison.
# Target: <= 256Mi (generous for a go-api at steady load; baseline is 32Mi).

NAMESPACE="${NAMESPACE:-go-api}"
APP="${APP:-go-api}"
MAX_MEMORY_MIB=256 # threshold in MiB

raw=$(kubectl get deployment "$APP" -n "$NAMESPACE" \
  -o jsonpath='{.spec.template.spec.containers[0].resources.requests.memory}' \
  2>/dev/null || true)

if [ -z "$raw" ]; then
  echo "FAIL: could not read memory request from ${NAMESPACE}/${APP}. Is the deployment running?" >&2
  exit 1
fi

# Convert to MiB: "256Mi" -> 256, "1Gi" -> 1024, "512M" -> ~488, "536870912" -> 512
to_mib() {
  local q="$1"
  case "$q" in
    *Gi) echo "${q%Gi}" | awk '{printf "%d", $1 * 1024}' ;;
    *Mi) echo "${q%Mi}" ;;
    *G) echo "${q%G}" | awk '{printf "%d", $1 * 953.674}' ;;
    *M) echo "${q%M}" | awk '{printf "%d", $1 / 1.04858}' ;;
    *Ki) echo "${q%Ki}" | awk '{printf "%d", $1 / 1024}' ;;
    # bare bytes
    *) echo "$q" | awk '{printf "%d", $1 / (1024*1024)}' ;;
  esac
}

actual_mib=$(to_mib "$raw")

if [ "$actual_mib" -le "$MAX_MEMORY_MIB" ]; then
  echo "OK: memory request ${raw} (${actual_mib}Mi) is within the right-sized threshold (<= ${MAX_MEMORY_MIB}Mi)."
else
  echo "FAIL: memory request ${raw} (${actual_mib}Mi) is over the right-sized threshold (<= ${MAX_MEMORY_MIB}Mi)." >&2
  echo "Right-size with: kubectl -n ${NAMESPACE} set resources deployment ${APP} --requests=cpu=50m,memory=32Mi" >&2
  exit 1
fi
