#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Injecting: deploying a CPU-burning neighbor with big requests and no limits..."
kubectl apply -f "$SCRIPT_DIR/manifests/noisy-neighbor.yaml"

echo "Injected. Node CPU will climb; go-api latency degrades under load."
echo "Tip: run 'labctl traffic start' to make the impact visible in Grafana."
