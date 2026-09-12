#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

# Grades the objective the drill previously only suggested: recovering prod from
# a bad release.
#
# The evidence is which ReplicaSet is serving. Rolling forward always creates a
# new one; going back — 'kubectl rollout undo', or setting the image to a tag you
# ran before — re-activates an OLDER one and bumps its revision. So a prod whose
# serving ReplicaSet is not the newest one ever created is a prod that went
# somewhere and came back. There is no way to fake that without actually doing
# it, and it does not care which of the two routes you took, because they are
# the same operation.

NS="env-prod"
APP="${WORKLOAD_NAME}"

if ! kubectl -n "$NS" get deploy "$APP" >/dev/null 2>&1; then
  echo "NOT COMPLETE: no ${APP} Deployment in ${NS}." >&2
  exit 1
fi

# Every ReplicaSet the Deployment has owned, oldest-created first.
BY_AGE="$(kubectl -n "$NS" get rs -l "app=${APP}" --sort-by=.metadata.creationTimestamp \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)"
COUNT="$(printf '%s\n' "$BY_AGE" | grep -c . || true)"

if [ "${COUNT:-0}" -lt 2 ]; then
  echo "PENDING: ${NS} has only had one ReplicaSet, so nothing has been rolled out to roll back." >&2
  echo "  Promote a release to prod first, then rehearse the recovery." >&2
  exit 1
fi

NEWEST="$(printf '%s\n' "$BY_AGE" | tail -1)"

# The ReplicaSet carrying the highest revision is the one serving; the newest by
# creation time is the last one that rolling FORWARD produced.
SERVING="$(kubectl -n "$NS" get rs -l "app=${APP}" \
  -o jsonpath='{range .items[*]}{.metadata.annotations.deployment\.kubernetes\.io/revision} {.metadata.name}{"\n"}{end}' \
  2>/dev/null | sort -rn | head -1)"
SERVING_RS="$(printf '%s' "$SERVING" | awk '{print $2}')"
SERVING_REV="$(printf '%s' "$SERVING" | awk '{print $1}')"

if [ -z "$SERVING_RS" ] || [ -z "$NEWEST" ]; then
  echo "NOT COMPLETE: could not read the ReplicaSets in ${NS}." >&2
  exit 1
fi

if [ "$SERVING_RS" = "$NEWEST" ]; then
  echo "PENDING: ${NS} is serving its newest ReplicaSet (${SERVING_RS}), so it has only ever rolled forward." >&2
  echo "  Rehearse the recovery. Ship something that cannot land, watch verify report the drift," >&2
  echo "  then undo it:" >&2
  echo "    kubectl -n ${NS} set image deployment/${APP} ${APP}=${APP}:v9.9.9" >&2
  echo "    labctl scenario verify env-promotion        # drift: spec says v9.9.9, pods do not" >&2
  echo "    kubectl -n ${NS} rollout undo deployment/${APP}" >&2
  echo "    kubectl -n ${NS} rollout status deployment/${APP}" >&2
  echo "  Then put env-metadata's declared_tag back to the release you rolled back to." >&2
  exit 1
fi

echo "OK: ${NS} is serving ${SERVING_RS} (revision ${SERVING_REV}), which is not the"
echo "newest ReplicaSet it has created (${NEWEST}). Prod went forward and was brought back —"
echo "and the rollback reused an existing ReplicaSet rather than building anything, which is why"
echo "it is the fastest lever you have in an incident."
