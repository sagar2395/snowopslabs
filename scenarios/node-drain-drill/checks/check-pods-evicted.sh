#!/usr/bin/env bash
# The drill's proof of work: a node that held workload pods before the drain
# holds none now.
#
# Read from the cluster, not from Prometheus. Draining a node evicts whatever
# lives on it — and when that node holds Prometheus's local-path volume, the
# monitoring stack goes Pending and records nothing for the window. A metric-
# based "was a node cordoned" check therefore has a hole exactly where the drill
# happens, and reports that nothing was drained.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

NS="${WORKLOAD_NAMESPACE}"
APP="${WORKLOAD_NAME}"
CM="${APP}-drain-drill-baseline"

BEFORE="$(kubectl -n "$NS" get configmap "$CM" -o 'jsonpath={.data.nodes}' 2>/dev/null || true)"
if [ -z "$BEFORE" ]; then
  echo "FAIL: no pre-drain placement was recorded, so the drain cannot be graded." >&2
  echo "  Re-run the staging step: labctl scenario up node-drain-drill --force" >&2
  exit 1
fi

NOW="$(kubectl -n "$NS" get pods -l "app=${APP}" --field-selector=status.phase=Running \
  -o 'jsonpath={range .items[*]}{.spec.nodeName}{"\n"}{end}' 2>/dev/null | sort -u || true)"

for node in $BEFORE; do
  if ! printf '%s\n' "$NOW" | grep -qx "$node"; then
    echo "OK: ${node} held ${APP} pods before the drain and holds none now — they were evicted and rescheduled."
    exit 0
  fi
done

echo "FAIL: every node that held ${APP} before still holds it, so no drain has moved a pod." >&2
echo "  before: ${BEFORE}" >&2
echo "  now:    $(printf '%s' "$NOW" | tr '\n' ' ')" >&2
echo "  Drain a node that is actually running one of these pods — draining an" >&2
echo "  empty node evicts nothing and teaches nothing:" >&2
echo "    kubectl -n $NS get pods -l app=$APP -o wide" >&2
echo "    kubectl cordon <node> && kubectl drain <node> --ignore-daemonsets --delete-emptydir-data" >&2
exit 1
