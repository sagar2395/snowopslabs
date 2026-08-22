#!/bin/bash

# Execute an application locally according to its build strategy.  Moves the
# 'go run' or 'docker run' commands out of the Makefile.

set -euo pipefail

APP_NAME=${1:?Usage: $0 <app-name>}

# load env if present
if [ -f "apps/${APP_NAME}/app.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "apps/${APP_NAME}/app.env"
  set +a
fi

# choose run mode
case "${BUILD_STRATEGY:-}" in
  docker)
    docker run --rm -p 8080:8080 "${APP_NAME}:latest"
    ;;
  *)
    # fallback assume Go project
    cd "apps/${APP_NAME}" && go run main.go
    ;;
esac
