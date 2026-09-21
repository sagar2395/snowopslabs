#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

BASELINE_TAG="v1.0.0"
APP="${WORKLOAD_NAME}"
STATE_DIR="${PROJECT_ROOT:-.}/.labctl/env-promotion"
STATE="${STATE_DIR}/pre-existing-tags"

# Record the versioned tags that already existed, so teardown removes only the
# ones this scenario built.
#
# The workload's own deployed image is a versioned tag too — go-api ships as
# go-api:v1.2.0 — and deleting it leaves the app unable to start on any node that
# does not already hold it. That is not hypothetical: it produced an
# ImagePullBackOff during a node roll, because the roll re-imports local images
# and this one was no longer there to import.
mkdir -p "$STATE_DIR"
docker images "$APP" --format '{{.Tag}}' 2>/dev/null |
  grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -u >"$STATE" || true
echo "Pre-existing ${APP} version tags (teardown will leave these alone): $(tr '\n' ' ' <"$STATE")"

echo "Seeding baseline image ${WORKLOAD_NAME}:${BASELINE_TAG}..."
# --import forces the load into the cluster (idempotent) so the env manifests,
# which pin <app>:${BASELINE_TAG} explicitly, always have a real image to pull.
DOCKER_IMAGE_TAG="${BASELINE_TAG}" bash src/engine/build/docker.sh ${WORKLOAD_NAME} --import
echo "✓ Baseline ${WORKLOAD_NAME}:${BASELINE_TAG} ready — environments will start here."
