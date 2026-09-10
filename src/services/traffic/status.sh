#!/usr/bin/env bash
# Show the traffic generator's current state and recent k6 output.
#
# The job is read once and every field derived from that snapshot. Reading it
# again for each line lets the report contradict itself: the job carries
# ttlSecondsAfterFinished, so it can be deleted between two calls, and the
# status then announces "running" and immediately fails with a raw NotFound.
set -euo pipefail

NAMESPACE="${TRAFFIC_NAMESPACE:-traffic}"

not_running() {
  echo "Traffic generator: not running"
  echo ""
  echo "Start one with:  labctl traffic start --profile steady --rps 20"
  exit 0
}

if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
  not_running
fi

JOB="$(kubectl get job traffic-k6 --namespace "$NAMESPACE" -o json 2>/dev/null || true)"
if [ -z "$JOB" ]; then
  # A finished run is cleaned up by ttlSecondsAfterFinished, so an absent job
  # after a completed run is expected rather than an error.
  not_running
fi

# Portable field reads from the one snapshot: no jq, which the lab does not
# require, and no second API call that could see different state.
# Missing keys are normal — a job that has not finished has no "succeeded" —
# so absence must read as empty, not as a failure that set -e turns fatal.
field() {
  printf '%s' "$JOB" | tr ',' '\n' | grep -m1 "\"$1\"" 2>/dev/null | sed 's/.*: *//; s/["{} ]//g' || true
}

PROFILE="$(field 'traffic-profile')"
SUCCEEDED="$(field 'succeeded')"
FAILED="$(field 'failed')"

if [ -n "${FAILED:-}" ] && [ "${FAILED:-0}" != "0" ]; then
  echo "Traffic generator: failed (profile: ${PROFILE:-unknown})"
elif [ -n "${SUCCEEDED:-}" ] && [ "${SUCCEEDED:-0}" != "0" ]; then
  echo "Traffic generator: completed (profile: ${PROFILE:-unknown})"
  echo "  The run finished. Start another with: labctl traffic start"
else
  echo "Traffic generator: running (profile: ${PROFILE:-unknown})"
fi
echo ""

# Everything below is best-effort: the job may be cleaned up while this runs,
# and a status command must never fail because of that.
kubectl get job traffic-k6 --namespace "$NAMESPACE" 2>/dev/null || true
echo ""
kubectl get pods --namespace "$NAMESPACE" -l app=traffic-k6 2>/dev/null || true
echo ""
echo "Recent k6 output:"
kubectl logs job/traffic-k6 --namespace "$NAMESPACE" --tail=15 2>/dev/null || echo "  (no logs available)"
