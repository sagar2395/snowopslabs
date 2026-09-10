#!/usr/bin/env bash
# Teardown for the restore marker, and for the drill state that only means
# anything while the drill is running.
#
# The archives go too. They are keyed to a marker token that teardown has just
# destroyed, so leaving them behind would hand the next activation a backup that
# looks valid and grades red — the worst kind of stale state.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_backup_lib.sh
. "${SCRIPT_DIR}/_backup_lib.sh"

NS="$(backup_ns "")"

kubectl -n "$NS" delete configmap restore-marker --ignore-not-found

DIR="$(backup_dir)"
if [ -d "$DIR" ]; then
  rm -f "${DIR}/${NS}"-*.json "$(bootid_log)" "$(loss_log)"
  echo "Removed the drill's backup archives and state from ${DIR}."
  rmdir "$DIR" 2>/dev/null || true
fi
