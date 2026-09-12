#!/usr/bin/env bash
set -eu
. "$(dirname "$0")/../../_lib/workload.sh"

# Objective: run an experiment against the workload.
#
# Chaos Mesh's gauge counts experiments that CURRENTLY EXIST, so grading it
# directly punishes the learner for following the drill's own last step —
# "clean up every experiment" sets it back to zero and turns the check red. It is
# also satisfied by an object a previous session left behind, which is why
# activation now clears those first.
#
# So the observation is recorded the first time it is seen, and the record is
# what the check reads afterwards.

NS="${WORKLOAD_NAMESPACE}"
WITNESS="chaos-experiment-witness"

FIRST="$(kubectl -n "$NS" get configmap "$WITNESS" -o jsonpath='{.data.first-seen}' 2>/dev/null || true)"

FOUND="$(kubectl -n "$NS" get podchaos,networkchaos,stresschaos \
  -o jsonpath='{range .items[*]}{.kind}/{.metadata.name} {end}' 2>/dev/null || true)"

if [ -n "${FOUND// /}" ]; then
  NOW="$(date -u +%s)"
  # first-seen answers "has an experiment ever run"; last-seen answers "was one
  # run since the workload was hardened", which is what blast-radius-contained
  # needs to tell a re-run from the original attack.
  kubectl -n "$NS" create configmap "$WITNESS" \
    --from-literal=first-seen="${FIRST:-$NOW}" \
    --from-literal=last-seen="$NOW" \
    --from-literal=detail="experiment(s) observed against ${NS}: ${FOUND}" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1 || true
  echo "OK: experiment(s) observed against ${NS}: ${FOUND}"
  exit 0
fi

if [ -n "$FIRST" ]; then
  seen="$(kubectl -n "$NS" get configmap "$WITNESS" -o jsonpath='{.data.detail}' 2>/dev/null || true)"
  echo "OK: ${seen} (recorded; the objects have since been cleaned up)"
  exit 0
fi

echo "PENDING: no chaos experiment has been seen in ${NS}." >&2
echo "  Put load on the service first — an experiment against an idle service proves nothing:" >&2
echo "    labctl traffic start --app ${WORKLOAD_NAME} --profile browse --rps 20 --duration 30m" >&2
echo "  Then inject exactly one failure and watch the blast radius:" >&2
echo "    bash scenarios/chaos-engineering/scripts/inject.sh pod-kill --app ${WORKLOAD_NAME} --namespace ${WORKLOAD_NAMESPACE}" >&2
exit 1
