#!/usr/bin/env bash
# Passes (exit 0) when the fault is resolved: the burner workload is gone.
set -euo pipefail
if kubectl get deployment noisy-neighbor -n labfault-noisy-neighbor >/dev/null 2>&1; then
  echo "noisy-neighbor deployment is still running"
  exit 1
fi
exit 0
