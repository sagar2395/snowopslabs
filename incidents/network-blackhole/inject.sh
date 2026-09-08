#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"

# shellcheck source=/dev/null
. "$(cd "$SCRIPT_DIR/../_lib" && pwd)/render.sh"

echo "Injecting: applying a deny-all-ingress NetworkPolicy in $NS..."
render_targeted "$SCRIPT_DIR/manifests/deny-ingress.yaml" | kubectl apply -f -

echo "Injected. Traffic to $DEPLOY through the ingress will now be dropped."
echo "(Pods stay Running and Ready — the break is on the wire, not in the app.)"
