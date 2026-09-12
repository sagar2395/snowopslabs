#!/usr/bin/env bash
# Teardown for the images this scenario built.
#
# It removes the versioned tags — and only those. ':latest' is the tag every
# other scenario's workload runs on, so deleting it here would break the lab
# from a teardown, which is the opposite of hygiene.
#
# Honest about its limits: an image already imported into the cluster's
# containerd store cannot be removed from outside the node, so it stays until
# the node is replaced. Saying so beats implying a clean slate that is not there.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

APP="${WORKLOAD_NAME}"
STATE="${PROJECT_ROOT:-.}/.labctl/env-promotion/pre-existing-tags"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is not available; nothing to clean up locally."
  exit 0
fi

# Only remove tags this scenario built. A versioned tag that was here before it
# started belongs to someone else — the workload's own deployed image is one —
# and deleting it strands the app on the next node that has to pull it.
REMOVED=0
for tag in $(docker images "$APP" --format '{{.Tag}}' 2>/dev/null || true); do
  case "$tag" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) continue ;;
  esac
  if [ -f "$STATE" ] && grep -qx "$tag" "$STATE"; then
    echo "Keeping ${APP}:${tag} — it was here before this scenario ran."
    continue
  fi
  if docker rmi "${APP}:${tag}" >/dev/null 2>&1; then
    echo "Removed local image ${APP}:${tag}"
    REMOVED=$((REMOVED + 1))
  fi
done

if [ "$REMOVED" -eq 0 ]; then
  echo "No versioned ${APP} images to remove."
fi
rm -f "$STATE" 2>/dev/null || true
echo "Note: copies already imported into the cluster's image store stay there until the node is replaced."
