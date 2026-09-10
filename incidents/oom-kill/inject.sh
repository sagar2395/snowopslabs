#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
MARK="labfault-oom-kill"

# The limit must sit between what the app uses at rest and what it needs under
# load, so the failure is the one that actually happens in production — healthy
# at rest, OOMKilled once real traffic arrives — rather than a pod that never
# starts. The traffic below is what makes it bite.
#
# Derive it from the workload's own memory request rather than hardcoding a
# number, because the fault follows a binding now and an absolute value
# calibrated for one application is simply wrong for the next: 8Mi reproduces
# this for a small Go service and would stop a JVM from ever starting.
# A quarter of the request lands in that window for a well-sized workload.
mem_to_mib() {
  # Kubernetes quantities: "32Mi", "1Gi", "512M", or plain bytes.
  awk -v q="$1" 'BEGIN {
    if (q ~ /Gi$/)      { sub(/Gi$/, "", q); printf "%d", q * 1024 }
    else if (q ~ /Mi$/) { sub(/Mi$/, "", q); printf "%d", q }
    else if (q ~ /G$/)  { sub(/G$/, "", q);  printf "%d", q * 1000000000 / 1048576 }
    else if (q ~ /M$/)  { sub(/M$/, "", q);  printf "%d", q * 1000000 / 1048576 }
    else if (q ~ /^[0-9]+$/) { printf "%d", q / 1048576 }
    else { printf "0" }
  }'
}

# Derive the limit from what the container ACTUALLY uses, not from what it
# declares. A fraction of the request is the obvious rule and it is wrong: a
# request is a guess someone made once, and workloads routinely over-declare —
# this repo's own Go service asks for 32Mi and uses under 3Mi, so a quarter of
# the request is 8Mi, which is below what it needs to START. The pod then never
# becomes Ready, which is a different and much less interesting failure than the
# one this fault is for: healthy at rest, killed once traffic arrives.
#
# Measured on that workload: ~2.8Mi at rest, ~5.7Mi serving 60 rps. A limit at
# 8Mi is killed during startup; 16Mi survives load comfortably; 12Mi — about
# four times the resting working set — starts cleanly and is OOMKilled under
# load, which is exactly the shape wanted. Hence the multiplier.
# Pod-level, not --containers: with --containers the columns shift (POD NAME CPU
# MEM) and reading the third field silently returns CPU, which parses as zero
# memory and falls back to the request-based rule this exists to replace.
# `|| true` is load-bearing: with `set -o pipefail`, `kubectl top` exiting
# non-zero because metrics-server has no sample for a just-rolled pod fails the
# whole assignment and aborts the script.
# Sample a few times and keep the largest. A reading taken in the first seconds
# after a rollout is cold — the same pod reported 1Mi and then 3Mi a minute
# later — and sizing the limit from the cold number produces one so tight the
# container cannot start, which is the failure this rule exists to avoid.
USED_MIB=0
for _ in 1 2 3; do
  sample="$(kubectl top pod -n "$NS" --no-headers 2>/dev/null |
    awk -v d="$DEPLOY" '$1 ~ d { print $3; exit }' || true)"
  mib="$(mem_to_mib "${sample:-0}")"
  if [ "${mib:-0}" -gt "$USED_MIB" ]; then
    USED_MIB="$mib"
    USED_RAW="$sample"
  fi
  sleep 3
done

REQUEST_RAW="$(kubectl -n "$NS" get deploy "$DEPLOY" \
  -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || true)"
REQUEST_MIB="$(mem_to_mib "${REQUEST_RAW:-0}")"

if [ "${USED_MIB:-0}" -ge 2 ]; then
  DERIVED_LIMIT_MIB=$((USED_MIB * 3))
  BASIS="its resting working set of ${USED_RAW:-unknown}"
elif [ "${REQUEST_MIB:-0}" -ge 8 ]; then
  # metrics-server is not answering. Fall back to the declared request, and let
  # the wait for the first OOMKill below report it if this misses.
  DERIVED_LIMIT_MIB=$((REQUEST_MIB / 4))
  BASIS="its declared request of ${REQUEST_RAW} (no metrics available)"
else
  DERIVED_LIMIT_MIB=12
  BASIS="a default calibrated for a small Go service"
fi
LIMIT_MIB="${DERIVED_LIMIT_MIB}"
FAULT_LIMIT="${LIMIT_MIB}Mi"
echo "Sizing the change from ${BASIS}."

# Trust the cluster, not just the marker. An annotation left behind by a partial
# resolve, or a limit someone restored by hand, makes the marker alone claim the
# fault is live when it is not — and the detection check then grades a healthy
# lab as already fixed.
CURRENT_LIMIT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true)"
if [ "$(kubectl -n "$NS" get deploy "$DEPLOY" -o "jsonpath={.metadata.annotations.$MARK}" 2>/dev/null)" = "injected" ] &&
  [ "$CURRENT_LIMIT" = "$FAULT_LIMIT" ]; then
  echo "Fault already injected — nothing to do."
  exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Arm the paging rule first so the on-call drill measures real detection.
# Tolerate a missing prometheus operator — the fault still works unpaged.
MON_NS="${MONITORING_NAMESPACE:-monitoring}"
# shellcheck source=/dev/null
. "$(cd "$(dirname "$0")/../_lib" && pwd)/render.sh"
render_targeted "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f - 2>/dev/null ||
  echo "Note: alert rule not installed (monitoring stack missing?) — continuing without paging."

# A literal "0" means the same as unset here, and it is a state an older resolve
# could leave behind. Recording it verbatim would tell the next resolve to put
# "0" back, so normalise it now.
none_if_unset() {
  case "${1:-}" in
    "" | 0) echo "none" ;;
    *) echo "$1" ;;
  esac
}

ORIG_LIMIT="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || true)"
ORIG_REQUEST="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || true)"

# Nothing printed here names the fault: the brief is fault.yaml's description
# and the page this armed.
echo "Applying a resource change to $NS/$DEPLOY..."
kubectl -n "$NS" annotate deploy "$DEPLOY" \
  "$MARK=injected" \
  "$MARK-original-limit=$(none_if_unset "$ORIG_LIMIT")" \
  "$MARK-original-request=$(none_if_unset "$ORIG_REQUEST")" \
  --overwrite >/dev/null

apply_limit() {
  kubectl -n "$NS" set resources "deploy/$DEPLOY" \
    --limits="memory=${1}Mi" --requests="memory=$((${1} / 2))Mi" >/dev/null

  # Let the new pod come up BEFORE judging it. Without this the rollout and the
  # traffic race: a container that would have served happily at this limit is
  # killed while it is still starting, and the fault becomes "a pod that never
  # becomes Ready" — a duller failure than the one being taught, and one that
  # appears only on some runs. Both outcomes were observed at the same limit.
  kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=90s >/dev/null 2>&1 || true
}

apply_limit "$LIMIT_MIB"

echo "Driving allocation with the k6 traffic generator..."
TRAFFIC_PROFILE=write \
  TRAFFIC_TARGET="http://$DEPLOY.$NS.svc.cluster.local:8080/" \
  TRAFFIC_RPS="${FAULT_RPS:-60}" \
  TRAFFIC_DURATION="${FAULT_DURATION:-30m}" \
  bash "$PROJECT_ROOT/src/services/traffic/start.sh"

# An injection that returns before the symptom exists is how this fault used to
# grade as already-fixed: the detection check ran in the window between the
# limit change and the first kill, passed, and closed the incident.
#
# The budget is an env knob because the wait needs a real cluster to ever end.
# Set FAULT_WAIT_SECONDS=0 to stage the fault without blocking on it — that is
# what the repo's script-contract test does, since it runs every fault script
# against a stub kubectl with no cluster behind it.
WAIT_SECONDS="${FAULT_WAIT_SECONDS:-300}"
POLL_SECONDS=5

if [ "$WAIT_SECONDS" -le 0 ]; then
  echo "Applied the change and started traffic; not waiting for it to take effect."
  exit 0
fi

# Which pods were ALREADY showing an OOMKill before this run? A previous
# injection leaves OOMKilled pods lying around for a while, and a namespace-wide
# scan for the word would find one of those and declare the fault live before it
# had done anything. Wait for a pod that was not already in that state.
oomkilled_pods() {
  kubectl -n "$NS" get pods \
    -o 'jsonpath={range .items[*]}{.metadata.name}{"="}{.status.containerStatuses[0].lastState.terminated.reason}{"\n"}{end}' \
    2>/dev/null | grep 'OOMKilled$' | cut -d= -f1 | sort || true
}
ALREADY=" $(oomkilled_pods | tr '\n' ' ') "

# Any OOMKilled pod that was not already in that state when this run started.
new_oomkill() {
  oomkilled_pods | while read -r pod; do
    [ -n "${pod:-}" ] || continue
    case "$ALREADY" in
      *" $pod "*) ;;
      *)
        echo "$pod"
        break
        ;;
    esac
  done
}

# Converge on a limit rather than predicting one.
#
# The usable window is narrow and workload-specific: it has to be above what the
# container needs to START and below what it reaches under load. For this repo's
# Go service those are about 9Mi and 15Mi, and a single `kubectl top` reading —
# 1Mi cold, 5Mi warm — multiplied by anything lands outside that window as often
# as inside it. So make the first attempt an estimate, then tighten and watch,
# which is what a person would do and is the only approach that survives being
# pointed at a workload nobody calibrated for.
echo "Waiting up to ${WAIT_SECONDS}s for the change to take effect..."
waited=0
round_budget=$((WAIT_SECONDS / 3))
[ "$round_budget" -ge 30 ] || round_budget="$WAIT_SECONDS"
round_spent=0

while [ "$waited" -lt "$WAIT_SECONDS" ]; do
  if [ -n "$(new_oomkill)" ]; then
    echo "Done."
    exit 0
  fi
  sleep "$POLL_SECONDS"
  waited=$((waited + POLL_SECONDS))
  round_spent=$((round_spent + POLL_SECONDS))

  # Nothing yet, and this round is up: the limit is too generous. Tighten it.
  if [ "$round_spent" -ge "$round_budget" ] && [ "$LIMIT_MIB" -gt 6 ]; then
    # Halve rather than shave. Each round costs a rollout as well as its poll
    # budget, so a 25% step spends the whole budget crawling towards the window
    # instead of landing in it — measured: 24 -> 18 -> 13 -> 9Mi and out of time.
    LIMIT_MIB=$((LIMIT_MIB / 2))
    [ "$LIMIT_MIB" -ge 6 ] || LIMIT_MIB=6
    echo "Still healthy — tightening to ${LIMIT_MIB}Mi."
    apply_limit "$LIMIT_MIB"
    FAULT_LIMIT="${LIMIT_MIB}Mi"
    round_spent=0
  fi
done

echo "The change is applied and traffic is running, but nothing happened in ${WAIT_SECONDS}s." >&2
echo "The fault is not live — the limit chosen (${FAULT_LIMIT}) may be too generous" >&2
echo "for this workload. Check that the traffic generator is running:" >&2
echo "  labctl traffic status" >&2
exit 1
