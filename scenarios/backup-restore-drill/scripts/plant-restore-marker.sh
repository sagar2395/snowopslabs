#!/usr/bin/env bash
# Plant the restore marker: a ConfigMap carrying a token unique to this
# activation.
#
# It is a script rather than a static manifest for one reason. If the marker were
# a file in this repository, "restore it" could be satisfied by re-applying that
# file — or by 'labctl scenario up --force', which restages it. Then the check
# would grade the object's existence, which was never in doubt, instead of the
# round-trip. A token minted here and nowhere else means the only way to put the
# marker back with the SAME value is to take it from the backup archive, which is
# what a restore is.
#
# Idempotent on purpose: an existing marker is left exactly as it is, so
# re-activating mid-drill does not silently invalidate the learner's archive.
set -euo pipefail

NS="${WORKLOAD_NAMESPACE:-${WORKLOAD_NAME:-go-api}}"

if kubectl -n "$NS" get configmap restore-marker >/dev/null 2>&1; then
  echo "restore-marker already present in ${NS}; leaving it untouched."
  exit 0
fi

TOKEN="$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"

kubectl -n "$NS" create configmap restore-marker \
  --from-literal=canary=do-not-delete \
  --from-literal=token="$TOKEN" \
  --from-literal=note="If this is missing or its token changed, the restore did not round-trip correctly."

kubectl -n "$NS" label configmap restore-marker \
  app.kubernetes.io/part-of=backup-restore-drill --overwrite >/dev/null

echo "Planted restore-marker in ${NS} with token ${TOKEN}."
echo "Back it up before you break anything: the archive is the only other copy of that token."
