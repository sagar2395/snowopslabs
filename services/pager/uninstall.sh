#!/usr/bin/env bash
set -euo pipefail

echo "Removing the lab pager..."
kubectl delete namespace pager --ignore-not-found
echo "Pager removed."
