#!/usr/bin/env bash
set -euo pipefail

# Docker Build and Import Script
# Usage: docker.sh <app-name> [--import] [--cluster-name] [--profile]

APP_NAME="${1:?Error: APP_NAME not provided}"

# source any app-specific configuration (optional)
if [ -f "apps/${APP_NAME}/app.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "apps/${APP_NAME}/app.env"
  set +a
fi

CLUSTER_NAME="${CLUSTER_NAME:-snowops}"
PROFILE="${PROFILE:-k3d}"

echo "Building Docker image for ${APP_NAME}..."
docker build -t "${APP_NAME}:latest" "apps/${APP_NAME}/"

# if we're targeting k3d, optionally import the image so the cluster
# can consume it.  when invoked from Make we don't pass --import, so we
# detect whether the image already exists and only import if necessary.
# (this also handles the case where Make passes --import explicitly.)
if [ "${PROFILE}" == "k3d" ]; then
  need_import=true
  if k3d image list -c "${CLUSTER_NAME}" 2>/dev/null | grep -q "^${APP_NAME}:latest"; then
    need_import=false
  fi
  if [ "${2:-}" == "--import" ]; then
    need_import=true
  fi

  if [ "$need_import" = true ]; then
    echo "Importing Docker image into k3d cluster '${CLUSTER_NAME}'..."
    k3d image import "${APP_NAME}:latest" -c "${CLUSTER_NAME}"
    echo "✓ Image imported successfully"
  else
    echo "Docker image already present in k3d, skipping import"
  fi
fi

echo "✓ Docker build complete"
