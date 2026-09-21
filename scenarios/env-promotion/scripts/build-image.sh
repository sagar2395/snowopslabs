#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

TAG="${1:?Usage: build-image.sh <tag>   e.g. build-image.sh v1.1.0}"

case "$TAG" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "ERROR: tag '${TAG}' should look like vMAJOR.MINOR.PATCH (e.g. v1.1.0)" >&2
    exit 1
    ;;
esac

echo "Building ${WORKLOAD_NAME}:${TAG} and loading it into the cluster..."
echo "  (this is 'docker build' + import — read src/engine/build/docker.sh)"
echo ""

DOCKER_IMAGE_TAG="${TAG}" bash src/engine/build/docker.sh ${WORKLOAD_NAME} --import

echo ""
echo "✓ ${WORKLOAD_NAME}:${TAG} is now available in the cluster."
echo ""
echo "Deploy it to dev and watch the rollout:"
echo "  kubectl -n env-dev set image deployment/${WORKLOAD_NAME} ${WORKLOAD_NAME}=${WORKLOAD_NAME}:${TAG}"
echo "  kubectl -n env-dev rollout status deployment/${WORKLOAD_NAME}"
echo "  kubectl -n env-dev patch cm env-metadata --type=merge -p '{\"data\":{\"declared_tag\":\"${TAG}\"}}'"
echo ""
echo "Then compare environments:"
echo "  curl -s ${WORKLOAD_NAME}-dev.<domain>/version      # new ${TAG}"
echo "  curl -s ${WORKLOAD_NAME}-staging.<domain>/version  # still the old version"
