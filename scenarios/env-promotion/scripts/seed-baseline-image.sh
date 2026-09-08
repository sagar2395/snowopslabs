#!/usr/bin/env bash
set -euo pipefail

BASELINE_TAG="v1.0.0"

echo "Seeding baseline image ${WORKLOAD_NAME:-go-api}:${BASELINE_TAG}..."
# --import forces the load into the cluster (idempotent) so the env manifests,
# which pin ${WORKLOAD_NAME:-go-api}:${BASELINE_TAG} explicitly, always have a real image to pull.
DOCKER_IMAGE_TAG="${BASELINE_TAG}" bash src/engine/build/docker.sh ${WORKLOAD_NAME:-go-api} --import
echo "✓ Baseline ${WORKLOAD_NAME:-go-api}:${BASELINE_TAG} ready — environments will start here."
