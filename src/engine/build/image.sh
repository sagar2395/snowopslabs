#!/usr/bin/env bash
set -euo pipefail

# Pre-built image strategy — for an application brought to the lab as a
# published image rather than as source. Nothing is compiled: the image is
# pulled once and imported into the local cluster, so it behaves exactly like a
# locally built one (offline, and with imagePullPolicy: Never).
#
# Usage: image.sh <app-name>
# Requires APP_IMAGE in apps/<app-name>/app.env, e.g.
#   BUILD_STRATEGY=image
#   APP_IMAGE=ghcr.io/acme/checkout-api:1.4.2

APP_NAME="${1:?Error: APP_NAME not provided}"

if [ -f "apps/${APP_NAME}/app.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "apps/${APP_NAME}/app.env"
  set +a
fi

if [ -z "${APP_IMAGE:-}" ]; then
  echo "ERROR: BUILD_STRATEGY=image requires APP_IMAGE in apps/${APP_NAME}/app.env" >&2
  echo "  e.g. APP_IMAGE=ghcr.io/acme/checkout-api:1.4.2" >&2
  exit 1
fi

CLUSTER_NAME="${CLUSTER_NAME:-snowops}"
PROFILE="${PROFILE:-k3d}"

# Import under the reference the image was published as. Retagging it locally
# produces a name whose manifest digest the cluster cannot resolve, and every pod
# then fails to start with the image apparently present.
echo "Pulling ${APP_IMAGE}..."
docker pull "${APP_IMAGE}"

case "${PROFILE}" in
  k3d)
    echo "Importing ${APP_IMAGE} into k3d cluster '${CLUSTER_NAME}'..."
    k3d image import "${APP_IMAGE}" -c "${CLUSTER_NAME}"
    ;;
  kind)
    echo "Loading ${APP_IMAGE} into kind cluster '${CLUSTER_NAME}'..."
    kind load docker-image "${APP_IMAGE}" --name "${CLUSTER_NAME}"
    ;;
  *)
    echo "Profile '${PROFILE}' needs no local import; the cluster pulls ${APP_IMAGE} itself."
    ;;
esac

echo "✓ Image ready as ${APP_IMAGE}"
