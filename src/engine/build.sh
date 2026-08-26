#!/bin/bash

# Wrapper for build strategies.  Loads the app's configuration and dispatches
# to the appropriate strategy script in engine/build/<strategy>.sh.
# Usage: build.sh <app-name> [options passed to strategy script]

set -euo pipefail

# The engine moved under src/ (issue #7). App configuration (apps/<name>/app.env)
# and app sources are still resolved from the working directory, which the
# caller sets to the content root — but the strategy scripts are siblings of
# this file, so locate them relative to the script, not the CWD.
ENGINE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

APP_NAME="${1:?Usage: $0 <app-name> [strategy-options]}"
shift || true # Remove APP_NAME, pass remaining args to strategy script

# Load app configuration to get BUILD_STRATEGY
if [ -f "apps/${APP_NAME}/app.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "apps/${APP_NAME}/app.env"
  set +a
fi

# Use explicit override if provided, otherwise use app.env default, otherwise fail
BUILD_STRATEGY="${BUILD_STRATEGY:?app.env must define BUILD_STRATEGY or pass BUILD_STRATEGY=... on command line}"

STRATEGY_SCRIPT="${ENGINE_DIR}/build/${BUILD_STRATEGY}.sh"

if [[ ! -f "${STRATEGY_SCRIPT}" ]]; then
  echo "[engine] ERROR: Strategy script not found: ${STRATEGY_SCRIPT}" >&2
  echo "[engine] Check BUILD_STRATEGY in apps/${APP_NAME}/app.env" >&2
  exit 1
fi

if [[ ! -x "${STRATEGY_SCRIPT}" ]]; then
  echo "[engine] ERROR: Strategy script is not executable: ${STRATEGY_SCRIPT}" >&2
  exit 1
fi

echo "[build] app=${APP_NAME} strategy=${BUILD_STRATEGY}"
bash "${STRATEGY_SCRIPT}" "${APP_NAME}" "$@"
