#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"

echo "Uninstalling Grafana..."

# Uninstall Grafana
helm uninstall grafana -n $NAMESPACE >/dev/null 2>&1 || true

echo "✓ Grafana uninstalled successfully"
