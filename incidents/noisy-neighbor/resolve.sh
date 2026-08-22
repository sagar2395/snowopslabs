#!/usr/bin/env bash
set -euo pipefail

echo "Evicting the noisy neighbor..."
kubectl delete namespace labfault-noisy-neighbor --ignore-not-found
echo "Resolved. Node CPU recovers as soon as the burner pods are gone."
