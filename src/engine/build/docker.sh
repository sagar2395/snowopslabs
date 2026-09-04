#!/usr/bin/env bash
set -euo pipefail

# Docker Build and Import Script
# Usage: docker.sh <app-name> [--import] [--cluster-name] [--profile]

APP_NAME="${1:?Error: APP_NAME not provided}"

CALLER_IMAGE_TAG="${DOCKER_IMAGE_TAG:-}"

# source any app-specific configuration (optional)
if [ -f "apps/${APP_NAME}/app.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "apps/${APP_NAME}/app.env"
  set +a
fi

# An explicit caller override wins over the app.env default.
if [ -n "${CALLER_IMAGE_TAG}" ]; then
  DOCKER_IMAGE_TAG="${CALLER_IMAGE_TAG}"
fi

CLUSTER_NAME="${CLUSTER_NAME:-snowops}"
PROFILE="${PROFILE:-k3d}"

IMAGE_TAG="${DOCKER_IMAGE_TAG:-latest}"
IMAGE_VERSIONED="${APP_NAME}:${IMAGE_TAG}"
IMAGE_LATEST="${APP_NAME}:latest"
BUILD_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

echo "Building Docker image ${IMAGE_VERSIONED}..."
docker build \
  --build-arg "VERSION=${IMAGE_TAG}" \
  --build-arg "COMMIT=${COMMIT}" \
  --build-arg "BUILD_DATE=${BUILD_DATE}" \
  -t "${IMAGE_VERSIONED}" \
  "apps/${APP_NAME}/"

# Point :latest at what we just built (only if we built a versioned tag).
if [ "${IMAGE_TAG}" != "latest" ]; then
  docker tag "${IMAGE_VERSIONED}" "${IMAGE_LATEST}"
fi

import_k3d() {
  echo "Importing ${IMAGE_VERSIONED} and ${IMAGE_LATEST} into k3d cluster '${CLUSTER_NAME}'..."
  k3d image import "${IMAGE_VERSIONED}" "${IMAGE_LATEST}" -c "${CLUSTER_NAME}"
  echo "✓ Image imported successfully"
}

import_kind() {
  echo "Loading ${IMAGE_VERSIONED} and ${IMAGE_LATEST} into kind cluster '${CLUSTER_NAME}'..."
  kind load docker-image "${IMAGE_VERSIONED}" "${IMAGE_LATEST}" --name "${CLUSTER_NAME}"
  echo "✓ Image loaded successfully"
}

if [ "${PROFILE}" == "k3d" ]; then
  need_import=true
  if k3d image list -c "${CLUSTER_NAME}" 2>/dev/null | grep -q "^${IMAGE_VERSIONED}$"; then
    need_import=false
  fi
  [ "${2:-}" == "--import" ] && need_import=true
  if [ "$need_import" = true ]; then
    import_k3d
  else
    echo "Docker image ${IMAGE_VERSIONED} already present in k3d, skipping import"
  fi
elif [ "${PROFILE}" == "kind" ]; then
  import_kind
fi

echo "✓ Docker build complete"
