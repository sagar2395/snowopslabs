#!/usr/bin/env bash
# Exactly one PodDisruptionBudget may select the workload's pods.
#
# The eviction API refuses a pod covered by two PDBs — "This pod has more than
# one PodDisruptionBudget, which the eviction subresource does not support" —
# and `kubectl drain` then fails partway through, having already evicted other
# pods. Several scenarios ship a PDB for the same workload (chaos-engineering
# and cluster-upgrade-drill both do), so this collides in ordinary use. The
# Kubernetes error names neither budget, which is why this check exists.
set -euo pipefail

NS="${WORKLOAD_NAMESPACE:-go-api}"
APP="${WORKLOAD_NAME:-go-api}"
MINE="${APP}-drill-pdb"

# A PDB covers this workload when its selector matches the app label the
# workload's pods carry. Reading matchLabels.app is enough here: every PDB the
# lab ships selects on it.
# A PDB covers this workload when its selector matches the app label the
# workload's pods carry. Reading matchLabels.app is enough here: every PDB the
# lab ships selects on it. custom-columns rather than jsonpath: a jsonpath
# filter expression cannot also emit newlines portably.
PDBS="$(kubectl -n "$NS" get pdb --no-headers \
  -o 'custom-columns=NAME:.metadata.name,APP:.spec.selector.matchLabels.app,OWNER:.metadata.labels.app\.kubernetes\.io/part-of' \
  2>/dev/null | awk -v app="$APP" '$2 == app { print }' || true)"
MATCHING="$(printf '%s' "$PDBS" | grep -c . || true)"

if [ "${MATCHING:-0}" -le 1 ]; then
  echo "OK: exactly ${MATCHING:-0} PodDisruptionBudget selects ${APP}."
  exit 0
fi

echo "FAIL: ${MATCHING} PodDisruptionBudgets select ${APP}'s pods:" >&2
printf '%s\n' "$PDBS" | awk '{ printf "    %s (from: %s)\n", $1, $3 }' >&2
echo "  The eviction API refuses a pod covered by more than one budget, so the" >&2
echo "  drain will fail partway through — after it has already evicted other pods." >&2
echo "  Another scenario left its budget behind. Deactivate it, or remove the one" >&2
echo "  that is not ${MINE}:" >&2
echo "    kubectl -n $NS get pdb" >&2
echo "    labctl scenario down <the other scenario>" >&2
exit 1
