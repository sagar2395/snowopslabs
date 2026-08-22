#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Injecting: applying a deny-all-ingress NetworkPolicy in go-api's namespace..."
kubectl apply -f "$SCRIPT_DIR/manifests/deny-ingress.yaml"

echo "Injected. Traffic to go-api through the ingress will now be dropped."
echo "(Pods stay Running and Ready — the break is on the wire, not in the app.)"
