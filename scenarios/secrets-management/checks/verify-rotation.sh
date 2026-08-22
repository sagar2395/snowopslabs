#!/usr/bin/env bash
set -euo pipefail

# Rotation-propagated check: the synced Kubernetes Secret must equal the value
# stage 2 wrote into Vault. kubectl decodes the value (portable, no host base64).
# Exit 0 = pass, non-zero = fail.

WANT="${SECRETS_ROTATED_VALUE:-scenario-secret-v2}"

GOT="$(kubectl -n go-api get secret go-api-secrets \
  -o go-template='{{index .data "api-key" | base64decode}}' 2>/dev/null || true)"

if [ "$GOT" = "$WANT" ]; then
  echo "OK: go-api-secrets/api-key propagated to rotated value."
  exit 0
fi

echo "FAIL: expected '${WANT}', got '${GOT:-<empty>}'." >&2
echo "Give ESO another refresh interval, or check the ExternalSecret status." >&2
exit 1
