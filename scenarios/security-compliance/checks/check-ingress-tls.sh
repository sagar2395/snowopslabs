#!/usr/bin/env bash
# shellcheck disable=SC2001  # sed is the clearest way to indent a multi-line block
set -euo pipefail

# Grades the last mile of the certificate task: a Certificate that reached Ready
# has proved cert-manager works, but nothing is actually protected until the
# Ingress serves it. So this asserts on the wire, not on the manifest.
#
# The handshake is the outcome. Traefik answers 443 either way — with its own
# CN=TRAEFIK DEFAULT CERT when no route matches, or with the lab-signed leaf
# once the Ingress references the Secret. Only the second one passes.

NS="go-api"
INGRESS="go-api"
HOST="go-api.${DOMAIN_SUFFIX:-k3d.local}"
SECRET="go-api-tls-secret"

if ! kubectl -n "$NS" get ingress "$INGRESS" >/dev/null 2>&1; then
  echo "FAIL: ingress/$INGRESS not found in $NS." >&2
  echo "      Deploy the app: labctl app deploy go-api" >&2
  exit 1
fi

tls_secret=$(kubectl -n "$NS" get ingress "$INGRESS" \
  -o jsonpath="{.spec.tls[?(@.secretName=='$SECRET')].secretName}" 2>/dev/null || echo "")

if [ "$tls_secret" != "$SECRET" ]; then
  echo "FAIL: ingress/$INGRESS does not reference the certificate Secret '$SECRET'." >&2
  echo "      A Ready Certificate protects nothing until the Ingress serves it. Wire it up:" >&2
  echo "        kubectl -n $NS patch ingress $INGRESS --type=merge -p \\" >&2
  echo "          '{\"metadata\":{\"annotations\":{\"traefik.ingress.kubernetes.io/router.entrypoints\":\"web,websecure\"}}," >&2
  echo "            \"spec\":{\"tls\":[{\"hosts\":[\"$HOST\"],\"secretName\":\"$SECRET\"}]}}'" >&2
  exit 1
fi

# What is actually presented on the wire for this SNI name?
served=$(echo | openssl s_client -connect "${HOST}:443" -servername "$HOST" 2>/dev/null |
  openssl x509 -noout -issuer -subject 2>/dev/null || echo "")

if [ -z "$served" ]; then
  echo "FAIL: nothing completed a TLS handshake at ${HOST}:443." >&2
  echo "      Check the ingress controller is listening on 443 and that $HOST resolves" >&2
  echo "      (sudo labctl hosts add), then retry." >&2
  exit 1
fi

case "$served" in
  *"issuer=CN=lab-ca"*) ;;
  *)
    echo "FAIL: ${HOST}:443 is served by a certificate the lab CA did not sign:" >&2
    echo "$served" | sed 's/^/        /' >&2
    echo "      'CN=TRAEFIK DEFAULT CERT' means the Ingress TLS block is not routing to" >&2
    echo "      $SECRET — check the host in spec.tls[].hosts matches $HOST exactly, and" >&2
    echo "      that the websecure entrypoint is enabled on the router." >&2
    exit 1
    ;;
esac

echo "${HOST}:443 is served by the lab-signed leaf:"
echo "$served" | sed 's/^/  /'
