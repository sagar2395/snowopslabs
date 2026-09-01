#!/usr/bin/env bash
set -euo pipefail

# observe-pv-data.sh [namespace] — print the boot-id the data-writer persisted on
# its PersistentVolume. Run it at each step of the drill to watch what a
# manifest backup does and does NOT protect:
#
#   before backup      -> boot-id: A
#   delete configmap   -> boot-id: A   (PV untouched)
#   restore            -> boot-id: A   (full recovery)
#   delete NAMESPACE   -> (pod gone)
#   restore            -> boot-id: B   (PVC object restored, but the volume — and
#                                       the data on it — is brand new: DATA LOST)
#
# This is the point of the drill: the ConfigMap round-trips, the PV data does not.

NS="${1:-go-api}"

if ! kubectl -n "$NS" get deploy data-writer >/dev/null 2>&1; then
  echo "data-writer is not deployed in '${NS}'." >&2
  echo "Activate the scenario first: labctl scenario up backup-restore-drill --deploy-prereqs" >&2
  exit 1
fi

POD=$(kubectl -n "$NS" get pod -l app=data-writer -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -z "$POD" ]; then
  echo "No data-writer pod is running in '${NS}' yet — the volume's data is unavailable." >&2
  echo "If you just deleted the namespace, restore first, then re-run this." >&2
  exit 1
fi

BOOT_ID=$(kubectl -n "$NS" exec "$POD" -- sh -c 'cat /data/boot-id 2>/dev/null' || true)
if [ -z "$BOOT_ID" ]; then
  echo "PV present but no boot-id yet (writer still starting). Re-run in a moment."
  exit 0
fi

echo "PV data boot-id: ${BOOT_ID}"
echo "(If this value changed after a restore, the PV data was LOST — a manifest"
echo " backup restores the PVC object, not the bytes that were on the volume.)"
