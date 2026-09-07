#!/usr/bin/env bash
set -euo pipefail

# reclaim-stranded-pvcs.sh — recover workloads left Pending after a node roll.
#
# local-path PersistentVolumes carry node affinity. When the node they are
# pinned to is replaced, the volume's data is gone but the PV and its claim
# survive, so the pod schedules nowhere and sits Pending forever with
# "didn't match PersistentVolume's node affinity".
#
# This deletes only volumes pinned to nodes that NO LONGER EXIST. Their data
# died with the node, so nothing recoverable is discarded — but it is still a
# destructive operation, which is why it is a step you run deliberately rather
# than something the roll does behind your back.
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

if [ -z "$STRANDED" ]; then
  echo "No stranded volumes: every local PersistentVolume is pinned to a node that still exists."
  exit 0
fi

echo "These volumes are pinned to nodes that no longer exist:"
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
echo "==> Restarting the Pending pods so their controllers recreate the claims"
kubectl get pods --all-namespaces --field-selector status.phase=Pending \
  -o jsonpath='{range .items[*]}{.metadata.namespace}{" "}{.metadata.name}{"\n"}{end}' 2>/dev/null |
  while IFS=' ' read -r ns pod; do
    [ -n "${pod:-}" ] || continue
    echo "    ${ns}/${pod}"
    kubectl -n "$ns" delete pod "$pod" --force --grace-period=0 >/dev/null 2>&1 || true
  done

echo ""
echo "Done. Watch them come back:  kubectl get pods -A --field-selector status.phase!=Running"
