#!/usr/bin/env bash
# One definition of "the consumer pod", shared by the activation that records the
# checkpoint and the check that grades against it.
#
# They cannot be allowed to disagree. A plain `-l app=secret-consumer` with
# items[0] will happily return a pod that is Terminating — which is exactly what
# happens on `scenario reset`, where the old Deployment's pod is still going away
# while the new one comes up. The checkpoint then names a pod that is already
# dead, and the drill grades red no matter what the learner does.

. "$(dirname "$0")/../../_lib/workload.sh"
NS="${WORKLOAD_NAMESPACE}"

# The newest Running pod that is not being deleted. --field-selector excludes
# Pending and Succeeded; the deletionTimestamp test excludes the one that is on
# its way out, which the phase alone does not.
consumer_pod_name() {
  kubectl -n "$NS" get pod -l "app=${1:-secret-consumer}" \
    --field-selector=status.phase=Running \
    --sort-by=.metadata.creationTimestamp \
    -o go-template='{{range .items}}{{if not .metadata.deletionTimestamp}}{{.metadata.name}} {{end}}{{end}}' \
    2>/dev/null | awk '{print $NF}'
}

consumer_pod_uid() {
  name="$(consumer_pod_name "${1:-secret-consumer}")"
  [ -n "$name" ] || return 0
  kubectl -n "$NS" get pod "$name" -o jsonpath='{.metadata.uid}' 2>/dev/null || true
}

# The drill's starting point, written by seed-baseline.sh. Empty means the
# scenario was never activated, or was activated before this field existed.
checkpoint() {
  kubectl -n "$NS" get configmap secret-rotation-checkpoint \
    -o "jsonpath={.data.$1}" 2>/dev/null || true
}
