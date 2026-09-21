#!/usr/bin/env bash
# Passes when the live restore marker carries the same token as the archive.
#
# This is the round-trip itself, not a proxy for it. The token exists in exactly
# two places — the cluster and the archive — so equality means the value in front
# of you came back from the backup. Re-staging the scenario mints a new token and
# turns this red, which is the point: 'labctl scenario up --force' is not a
# restore.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_backup_lib.sh
. "${SCRIPT_DIR}/../scripts/_backup_lib.sh"

NS="$(backup_ns "")"
ARCHIVE="$(archive_path "$NS")"
RESTORE_CMD="  bash scenarios/backup-restore-drill/scripts/restore.sh ${NS}"

if [ ! -f "$ARCHIVE" ]; then
  echo "FAIL: no archive at ${ARCHIVE} — there is nothing to have restored from." >&2
  echo "  You cannot restore what you never captured. Back up first, then break something." >&2
  exit 1
fi
require_jq || exit 1

ARCHIVED_TOKEN="$(jq -r '.items[]
  | select(.kind == "ConfigMap" and .metadata.name == "restore-marker")
  | .data.token // empty' "$ARCHIVE" 2>/dev/null | head -1)"
[ -n "$ARCHIVED_TOKEN" ] || {
  echo "FAIL: the archive has no restore-marker to round-trip. Re-take the backup while the marker exists." >&2
  exit 1
}

LIVE_TOKEN="$(kubectl -n "$NS" get configmap restore-marker -o jsonpath='{.data.token}' 2>/dev/null || true)"
if [ -z "$LIVE_TOKEN" ]; then
  echo "FAIL: restore-marker is missing from ${NS}. Restore it from the archive:" >&2
  echo "$RESTORE_CMD" >&2
  exit 1
fi

CANARY="$(kubectl -n "$NS" get configmap restore-marker -o jsonpath='{.data.canary}' 2>/dev/null || true)"
if [ "$LIVE_TOKEN" != "$ARCHIVED_TOKEN" ] || [ "$CANARY" != "do-not-delete" ]; then
  echo "FAIL: the marker in ${NS} is not the one in the archive." >&2
  echo "  archive: token=${ARCHIVED_TOKEN}" >&2
  echo "  live:    token=${LIVE_TOKEN} canary=${CANARY}" >&2
  echo "  A marker that was re-created rather than restored carries a different token." >&2
  echo "  Apply the archive:" >&2
  echo "$RESTORE_CMD" >&2
  exit 1
fi

echo "OK: the live marker carries the archive's token (${ARCHIVED_TOKEN}) — the objects round-tripped."
