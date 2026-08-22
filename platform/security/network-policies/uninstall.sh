#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"

echo "Removing network policies..."

kubectl delete -f "$SCRIPT_DIR/allow-ingress.yaml" --ignore-not-found >/dev/null 2>&1 || true
kubectl delete -f "$SCRIPT_DIR/allow-monitoring.yaml" --ignore-not-found >/dev/null 2>&1 || true
kubectl delete -f "$SCRIPT_DIR/allow-dns.yaml" --ignore-not-found >/dev/null 2>&1 || true
kubectl delete -f "$SCRIPT_DIR/default-deny.yaml" --ignore-not-found >/dev/null 2>&1 || true

echo "Network policies removed successfully"
