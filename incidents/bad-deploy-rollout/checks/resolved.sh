#!/usr/bin/env bash
# Passes (exit 0) when the fault is resolved: the workload is serving again.
set -euo pipefail
NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"

# shellcheck source=/dev/null
. "$(cd "$(dirname "$0")/../../_lib" && pwd)/assert.sh"

assert_workload_healthy "$NS" "$DEPLOY"
