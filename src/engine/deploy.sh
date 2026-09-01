#!/bin/bash

# Wrapper for deploy strategies.  Loads the app's configuration and dispatches
# to the appropriate strategy script in engine/deploy/<strategy>.sh.
# Usage: deploy.sh <command> <app-name> [options passed to strategy script]

set -euo pipefail

# The engine moved under src/ (issue #7). App config and sources are resolved
# from the caller's working directory (the content root); the strategy scripts
# are siblings of this file, so locate them relative to the script.
ENGINE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

COMMAND="${1:?Usage: $0 <command> <app-name>}"
APP_NAME="${2:?Usage: $0 <command> <app-name>}"
shift 2 || true # Remove COMMAND and APP_NAME, pass remaining args to strategy

# Load app configuration to get DEPLOY_STRATEGY
if [ -f "apps/${APP_NAME}/app.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "apps/${APP_NAME}/app.env"
  set +a
fi

# Use explicit override if provided, otherwise use app.env default, otherwise fail
DEPLOY_STRATEGY="${DEPLOY_STRATEGY:?app.env must define DEPLOY_STRATEGY or pass DEPLOY_STRATEGY=... on command line}"

# A docker-built image lives only in the host Docker daemon; on k3d/kind it must
# be loaded into the cluster or the pod hits ImagePullBackOff. `app build` did
# this but `app deploy` didn't, so a deploy after a cluster re-create failed.
# Load the image into the cluster before deploying.
ensure_image_in_cluster() {
  [ "${COMMAND}" = "deploy" ] || return 0
  [ "${BUILD_STRATEGY:-}" = "docker" ] || return 0

  local profile="${PROFILE:-k3d}"
  local cluster="${CLUSTER_NAME:-snowops}"
  local image="${APP_NAME}:${DOCKER_IMAGE_TAG:-latest}"

  case "${profile}" in
    k3d)
      command -v k3d >/dev/null 2>&1 || return 0
      if ! docker image inspect "${image}" >/dev/null 2>&1; then
        echo "[deploy] Image ${image} not found in the local Docker daemon — building it first..."
        bash "${ENGINE_DIR}/build.sh" "${APP_NAME}" # build.sh imports into k3d itself
        return 0
      fi
      # Import is idempotent, so run it unconditionally rather than depend on the
      # `k3d image list` output format (which varies across k3d versions).
      echo "[deploy] Importing ${image} into k3d cluster '${cluster}'..."
      k3d image import "${image}" -c "${cluster}"
      ;;
    kind)
      command -v kind >/dev/null 2>&1 || return 0
      if ! docker image inspect "${image}" >/dev/null 2>&1; then
        echo "[deploy] Image ${image} not found in the local Docker daemon — building it first..."
        bash "${ENGINE_DIR}/build.sh" "${APP_NAME}" # docker build only; kind load happens below
      fi
      # kind load is idempotent; the docker build strategy does not load into kind.
      echo "[deploy] Loading ${image} into kind cluster '${cluster}'..."
      kind load docker-image "${image}" --name "${cluster}"
      ;;
    *)
      # incluster / registry-backed runtimes pull from a real registry — nothing to import.
      :
      ;;
  esac
}

ensure_image_in_cluster

STRATEGY_SCRIPT="${ENGINE_DIR}/deploy/${DEPLOY_STRATEGY}.sh"

if [[ ! -f "${STRATEGY_SCRIPT}" ]]; then
  echo "[engine] ERROR: Strategy script not found: ${STRATEGY_SCRIPT}" >&2
  echo "[engine] Check DEPLOY_STRATEGY in apps/${APP_NAME}/app.env" >&2
  exit 1
fi

if [[ ! -x "${STRATEGY_SCRIPT}" ]]; then
  echo "[engine] ERROR: Strategy script is not executable: ${STRATEGY_SCRIPT}" >&2
  exit 1
fi

echo "[${COMMAND}] app=${APP_NAME} strategy=${DEPLOY_STRATEGY}"
bash "${STRATEGY_SCRIPT}" "${COMMAND}" "${APP_NAME}" "$@"
