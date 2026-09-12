#!/usr/bin/env bash
# inject.sh <experiment> — apply exactly one chaos experiment to the bound
# workload. Run with no argument to list what is available.
#
# This exists because the experiment manifests are the one set of manifests the
# scenario deliberately does NOT install as a component: applying all six at once
# kills, delays and stresses the workload simultaneously, and nothing can be
# attributed. But that also means nothing ever renders their {{.WorkloadName}}
# and {{.WorkloadNamespace}} — a plain `kubectl apply -f` on the file fails with
# `namespaces "{{.WorkloadNamespace}}" not found`, because kubectl does not
# resolve Go templates. So the rendering happens here, and the learner still runs
# one experiment at a time.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MANIFEST="${SCRIPT_DIR}/../manifests/chaos-experiments.yaml"
NS="${WORKLOAD_NAMESPACE}"
WORKLOAD="${WORKLOAD_NAME}"

available() {
  sed -n 's/^ *experiment: *\([a-z-]*\).*/\1/p' "$MANIFEST" | sort -u
}

EXPERIMENT="${1:-}"
if [ -z "$EXPERIMENT" ]; then
  echo "Usage: inject.sh <experiment>"
  echo ""
  echo "Available:"
  available | sed 's/^/  /'
  echo ""
  echo "Run ONE at a time. Two failures at once make the blast radius unattributable,"
  echo "which is the one thing chaos engineering must never be."
  exit 1
fi

if ! available | grep -qx "$EXPERIMENT"; then
  echo "ERROR: no experiment named '${EXPERIMENT}'." >&2
  echo "Available: $(available | tr '\n' ' ')" >&2
  exit 1
fi

# Render the workload binding, then let kubectl pick the one experiment by label.
rendered="$(sed -e "s|{{\.WorkloadName}}|${WORKLOAD}|g" -e "s|{{\.WorkloadNamespace}}|${NS}|g" "$MANIFEST")"

echo "Injecting '${EXPERIMENT}' against ${NS}/${WORKLOAD}..."
printf '%s\n' "$rendered" | kubectl apply -f - -l "experiment=${EXPERIMENT}"

echo ""
echo "Watch the blast radius while it runs:"
echo "  kubectl -n ${NS} get pods -w"
echo "  http://grafana.${DOMAIN_SUFFIX:-k3d.local}/d/chaos-engineering"
echo ""
echo "Then grade what you measured:  labctl scenario verify chaos-engineering"
