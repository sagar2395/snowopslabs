#!/usr/bin/env bash
# Shared reading of the drill's checkpoint.
#
# Three checks need the same answer — "has this cluster actually been rolled?" —
# and none of them can get it from live state alone: an untouched cluster has
# every node on one version, which is precisely what a finished roll looks like.
# The checkpoint stage-ha.sh writes is the only thing that tells them apart.

. "$(dirname "$0")/../../_lib/workload.sh"
NS="${WORKLOAD_NAMESPACE}"
STATE="upgrade-drill-baseline"

checkpoint() {
  kubectl -n "$NS" get configmap "$STATE" -o "jsonpath={.data.$1}" 2>/dev/null || true
}

# The worker nodes recorded at activation that are STILL the same node.
#
# Compared by UID, not name: k3d reuses a node's name whenever it can, so a
# genuinely rolled worker usually comes back called exactly what it was. Names
# are carried alongside only so the failure message can name what is left.
unrolled_workers() {
  uids="$(checkpoint worker-uids)"
  names="$(checkpoint workers)"
  [ -n "$uids" ] || return 0
  live="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.uid}{"\n"}{end}' 2>/dev/null || true)"
  i=0
  for u in $uids; do
    i=$((i + 1))
    if printf '%s\n' "$live" | grep -qx "$u"; then
      printf '%s ' "$(printf '%s\n' $names | sed -n "${i}p")"
    fi
  done
  return 0
}

# Epoch at which a roll was first observed, recorded on the fly so the
# availability window can start there rather than at a fixed offset.
mark_roll_started() {
  existing="$(checkpoint roll-started-at)"
  if [ -z "$existing" ]; then
    kubectl -n "$NS" patch configmap "$STATE" --type merge \
      -p "{\"data\":{\"roll-started-at\":\"$(date -u +%s)\"}}" >/dev/null 2>&1 || true
    existing="$(date -u +%s)"
  fi
  printf '%s' "$existing"
}
