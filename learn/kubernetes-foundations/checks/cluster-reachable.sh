#!/usr/bin/env bash
# Passes when the Kubernetes API server is reachable.
set -euo pipefail
kubectl cluster-info >/dev/null 2>&1
