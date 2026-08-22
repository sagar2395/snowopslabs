#!/usr/bin/env bash
set -euo pipefail

# In-cluster runtime "down" — no-op. The labctl server does not own the cluster
# it runs in, so it must never tear it down. Removing the server is done by
# uninstalling its Helm release:
#   helm uninstall labctl-server -n labctl

echo "In-cluster runtime: refusing to delete the hosting cluster (by design)."
echo "To remove the server: helm uninstall labctl-server -n <namespace>"
