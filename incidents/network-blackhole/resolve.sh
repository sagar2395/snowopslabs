#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"

echo "Removing the deny-all-ingress NetworkPolicy..."
kubectl delete networkpolicy labfault-network-blackhole -n "$NS" --ignore-not-found
echo "Resolved. Traffic should flow again immediately."
