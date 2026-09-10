#!/usr/bin/env bash
# Record the workload's security posture as the scenario found it.
#
# The drill's second objective is "remediate the workload at the source", which
# only exists while the workload is NOT yet compliant. The workload is shared, so
# a previous run of this scenario that left its own remediation in place hands
# the next learner a policy report with nothing to fix — objective 2 green before
# they start.
#
# Recording rather than forcing: this scenario does not get to decide how the
# shared app is configured, only to put it back the way it was.
set -euo pipefail

NS="${WORKLOAD_NAMESPACE:-go-api}"
WORKLOAD="${WORKLOAD_NAME:-go-api}"
STATE="security-baseline"

kubectl -n "$NS" get deploy "$WORKLOAD" >/dev/null 2>&1 || {
  echo "WARNING: ${NS}/${WORKLOAD} is not deployed; nothing to record." >&2
  exit 0
}

CTX="$(kubectl -n "$NS" get deploy "$WORKLOAD" \
  -o jsonpath='{.spec.template.spec.containers[0].securityContext}' 2>/dev/null || true)"

# Only once: a --force re-activation would otherwise record the learner's own
# remediation as the baseline, and teardown would leave it in place — which is
# exactly the state that pre-completes objective 2 for the next learner.
if kubectl -n "$NS" get configmap "$STATE" >/dev/null 2>&1; then
  echo "Keeping the securityContext recorded at the first activation."
else
  kubectl -n "$NS" create configmap "$STATE" \
    --from-literal=security-context="${CTX:-null}" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  echo "Recorded ${WORKLOAD}'s securityContext so teardown can restore it: ${CTX:-<none>}"
fi
