#!/usr/bin/env bash
# Passes (exit 0) when the fault is resolved: the rollout completes.
set -euo pipefail
NS="${TARGET_NAMESPACE:-echo-server}"
DEPLOY="${TARGET_WORKLOAD:-echo-server}"
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=15s
