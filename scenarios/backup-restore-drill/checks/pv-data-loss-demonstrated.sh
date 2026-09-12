#!/usr/bin/env bash
# Passes once the volume's data has demonstrably NOT survived a restore.
#
# This is the lesson the whole drill exists for, so it is graded rather than left
# as a tip. The backup records the data-writer's boot-id at backup time; a
# restored PVC binds to a brand-new empty volume, so the writer mints a different
# one. Equal boot-ids mean the volume was never actually lost, and the learner
# has not yet seen the gap between "the object came back" and "the data came
# back".
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/_backup_lib.sh
. "${SCRIPT_DIR}/../scripts/_backup_lib.sh"

NS="$(backup_ns "")"
LOG="$(bootid_log "$NS")"

HOW=$(
  cat <<TXT
  Destroy the volume and restore the objects, then re-verify:
    kubectl -n ${NS} delete deploy data-writer
    kubectl -n ${NS} delete pvc restore-data
    bash scenarios/backup-restore-drill/scripts/restore.sh ${NS}
    bash scenarios/backup-restore-drill/scripts/observe-pv-data.sh ${NS}
TXT
)

if [ ! -s "$LOG" ]; then
  echo "FAIL: no boot-id was recorded, so there is no 'before' to compare against." >&2
  echo "  Take the backup while the data-writer is running — that is when the fingerprint is captured:" >&2
  echo "    bash scenarios/backup-restore-drill/scripts/backup.sh ${NS}" >&2
  exit 1
fi

# The FIRST recorded value is the one that matters: a second backup taken after
# the restore would record the new boot-id, and comparing against that would
# quietly erase the evidence the learner just produced.
AT_BACKUP="$(head -1 "$LOG" | awk '{print $2}')"
LIVE="$(live_boot_id "$NS")"

if [ -z "$LIVE" ]; then
  echo "FAIL: no data-writer pod is serving a boot-id in ${NS} yet." >&2
  echo "  If you just restored, give the pod a moment to bind its new volume and start." >&2
  echo "$HOW" >&2
  exit 1
fi

if [ "$LIVE" = "$AT_BACKUP" ]; then
  echo "FAIL: the volume still holds the original data (boot-id ${LIVE})." >&2
  echo "  The PV-data lesson has not been rehearsed: so far every restore has been of objects" >&2
  echo "  whose volume was never touched." >&2
  echo "$HOW" >&2
  exit 1
fi

echo "OK: boot-id was ${AT_BACKUP} at backup time and is ${LIVE} now."
echo "The PVC object was restored from the archive; the bytes on the volume were not."
