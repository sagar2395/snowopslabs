#!/usr/bin/env bash
# Passes when a backup archive exists that could actually restore this drill.
#
# "The file exists" is not that. An empty file created by `touch` satisfies a
# file test and restores nothing, so this reads the archive: it must parse, hold
# a List of objects, and contain the restore marker carrying the token this
# activation minted. That last part is what ties the archive to the drill in
# front of the learner rather than to some run from last week.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_backup_lib.sh
. "${SCRIPT_DIR}/../scripts/_backup_lib.sh"

NS="$(backup_ns "")"
ARCHIVE="$(archive_path "$NS")"

fail() {
  echo "FAIL: $1" >&2
  echo "  Take a backup of the live namespace: bash scenarios/backup-restore-drill/scripts/backup.sh ${NS}" >&2
  exit 1
}

[ -f "$ARCHIVE" ] || fail "no backup archive at ${ARCHIVE}."
require_jq || exit 1

jq -e '.kind == "List"' "$ARCHIVE" >/dev/null 2>&1 ||
  fail "${ARCHIVE} is not a manifest archive (expected a JSON List). An empty or truncated file restores nothing."

COUNT="$(jq '.items | length' "$ARCHIVE" 2>/dev/null || echo 0)"
[ "${COUNT:-0}" -ge 1 ] || fail "${ARCHIVE} holds no objects, so there is nothing to restore."

ARCHIVED_TOKEN="$(jq -r '.items[]
  | select(.kind == "ConfigMap" and .metadata.name == "restore-marker")
  | .data.token // empty' "$ARCHIVE" 2>/dev/null | head -1)"

if [ -z "$ARCHIVED_TOKEN" ]; then
  fail "${ARCHIVE} does not contain the restore-marker ConfigMap, so it cannot prove a round-trip."
fi

LIVE_TOKEN="$(kubectl -n "$NS" get configmap restore-marker -o jsonpath='{.data.token}' 2>/dev/null || true)"
if [ -n "$LIVE_TOKEN" ] && [ "$LIVE_TOKEN" != "$ARCHIVED_TOKEN" ]; then
  echo "FAIL: the archive holds marker token ${ARCHIVED_TOKEN}, but the live marker is ${LIVE_TOKEN}." >&2
  echo "  This archive predates the current marker — restoring it would put back the wrong value." >&2
  echo "  Take a fresh backup: bash scenarios/backup-restore-drill/scripts/backup.sh ${NS}" >&2
  exit 1
fi

echo "OK: ${ARCHIVE} holds ${COUNT} object(s) including the restore marker (token ${ARCHIVED_TOKEN})."
