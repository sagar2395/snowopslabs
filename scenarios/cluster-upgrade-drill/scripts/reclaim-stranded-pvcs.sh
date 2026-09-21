#!/usr/bin/env bash
set -euo pipefail

# reclaim-stranded-pvcs.sh — recover workloads left Pending after a node roll.
#
# local-path PersistentVolumes carry node affinity, and a node roll strands them
# in one of two ways. Both are handled here, because in practice the second is
# the common one and used to be invisible:
#
#   1. The replacement node has a NEW NAME. The PV's affinity names a node that
#      no longer exists, so the pod schedules nowhere and sits Pending with
#      "didn't match PersistentVolume's node affinity".
#
#   2. The replacement node REUSES THE NAME — which is what k3d does whenever it
#      can. The affinity still matches, so the pod schedules happily and then
#      cannot mount, because the directory the volume lived in went with the old
#      container: "MountVolume.NewMounter initialization failed ... path ...
#      does not exist". A node-existence test sees nothing wrong here and
#      reports success while the pod is stuck for ever.
#
# This deletes only volumes whose data is already gone, so nothing recoverable is
# discarded — but it is still a destructive operation, which is why it is a step
# you run deliberately rather than something the roll does behind your back.
#
# On a real cluster the lesson is the same one, learned earlier: workloads that
# must survive a node roll need replicated or network-attached storage.

DRY_RUN="${DRY_RUN:-false}"

LIVE_NODES="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')"

STRANDED=""
while IFS=' ' read -r pv claim node; do
  [ -n "${pv:-}" ] || continue
  [ -n "${node:-}" ] || continue
  if ! echo "$LIVE_NODES" | grep -qx "$node"; then
    STRANDED="${STRANDED}${pv} ${claim} ${node}"$'\n'
  fi
done <<EOF
$(kubectl get pv -o jsonpath="{range .items[?(@.spec.nodeAffinity)]}{.metadata.name}{' '}{.spec.claimRef.namespace}/{.spec.claimRef.name}{' '}{.spec.nodeAffinity.required.nodeSelectorTerms[0].matchExpressions[0].values[0]}{'\n'}{end}" 2>/dev/null)
EOF

# Case 2: the kubelet is telling us the backing directory is gone. This is the
# only reliable signal for a name-reusing roll — the PV, the claim and the node
# all look perfectly healthy.
GONE_PATHS="$(kubectl get events --all-namespaces --field-selector reason=FailedMount \
  -o jsonpath='{range .items[*]}{.message}{"\n"}{end}' 2>/dev/null |
  sed -n 's/.*volume "\([^"]*\)".*does not exist.*/\1/p' | sort -u || true)"

for pv in $GONE_PATHS; do
  # Skip anything already caught by the node-existence test above.
  case "$STRANDED" in *"$pv "*) continue ;; esac
  claim="$(kubectl get pv "$pv" \
    -o jsonpath='{.spec.claimRef.namespace}/{.spec.claimRef.name}' 2>/dev/null || true)"
  node="$(kubectl get pv "$pv" \
    -o jsonpath='{.spec.nodeAffinity.required.nodeSelectorTerms[0].matchExpressions[0].values[0]}' 2>/dev/null || true)"
  [ -n "${claim:-}" ] && [ "$claim" != "/" ] || continue
  STRANDED="${STRANDED}${pv} ${claim} ${node:-unknown}"$'\n'
done

if [ -z "$STRANDED" ]; then
  echo "No stranded volumes: every local PersistentVolume is pinned to a node that still"
  echo "exists, and no pod is reporting a missing backing directory."
  exit 0
fi

echo "These volumes have lost their data to a node roll:"
echo "$STRANDED" | sed '/^$/d' | awk '{print "  "$2"  (was on "$3")"}'
echo ""

if [ "$DRY_RUN" = "true" ]; then
  echo "DRY_RUN=true — nothing deleted."
  exit 0
fi

echo "$STRANDED" | sed '/^$/d' | while IFS=' ' read -r pv claim node; do
  ns="${claim%%/*}"
  name="${claim##*/}"
  echo "==> Releasing ${claim} (data was lost with ${node})"
  kubectl -n "$ns" delete pvc "$name" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete pv "$pv" --ignore-not-found --wait=false >/dev/null 2>&1 || true
done

# The owning StatefulSet recreates the claim, but only when its pod restarts.
# A name-reusing roll leaves the pod Pending OR stuck in ContainerCreating, so
# restart anything that is not Running rather than only the Pending ones.
echo "==> Restarting the stuck pods so their controllers recreate the claims"
kubectl get pods --all-namespaces \
  -o jsonpath='{range .items[?(@.status.phase!="Running")]}{.metadata.namespace}{" "}{.metadata.name}{"\n"}{end}' 2>/dev/null |
  grep -v '^$' |
  while IFS=' ' read -r ns pod; do
    [ -n "${pod:-}" ] || continue
    echo "    ${ns}/${pod}"
    kubectl -n "$ns" delete pod "$pod" --force --grace-period=0 >/dev/null 2>&1 || true
  done

echo ""
echo "Done. Watch them come back:  kubectl get pods -A --field-selector status.phase!=Running"
