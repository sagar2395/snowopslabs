#!/usr/bin/env bash
# Remove every chaos experiment from the workload's namespace.
#
# Safe to run at any point: the drill's grades are recorded when an experiment
# is observed, not read from the objects, so clearing them does not undo work
# you have already done.
set -euo pipefail

NS="${WORKLOAD_NAMESPACE:-go-api}"

kubectl -n "$NS" delete podchaos,networkchaos,stresschaos --all --ignore-not-found
echo "All chaos experiments removed from ${NS}."
