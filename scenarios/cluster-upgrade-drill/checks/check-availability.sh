#!/usr/bin/env bash
set -eu

# Grades the drill's headline claim: the request success rate held across the
# upgrade window.
#
# It replaces a promql check that divided success rate by total rate. That form
# reports a perfect 1.00 when there is no traffic at all — 0/0 guarded to 1 — so
# the scenario's central grade was green on an untouched, idle cluster while the
# 'traffic-is-flowing' check sitting next to it correctly said there was nothing
# to measure. Two checks, one truth, and the important one was wrong.
#
# Three things have to be true before a number here means anything: a roll has
# started, there was real load while it happened, and the window being measured
# begins at the roll rather than an arbitrary ten minutes ago.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=checks/_drill.sh
. "${SCRIPT_DIR}/_drill.sh"

WORKLOAD="${WORKLOAD_NAME:-go-api}"
METRIC="${WORKLOAD_METRIC:-http_server_request_duration_seconds}"
PROM="${PROMETHEUS_URL:-http://prometheus.${DOMAIN_SUFFIX:-k3d.local}}"
SLO="${AVAILABILITY_SLO:-0.99}"
MIN_RPS="${UPGRADE_MIN_RPS:-0.5}"

promq() {
  curl -s --max-time 25 --get "${PROM}/api/v1/query" --data-urlencode "query=$1" |
    sed -n 's/.*"value":\[[0-9.]*,"\([^"]*\)"\].*/\1/p'
}

RECORDED="$(checkpoint worker-uids)"
if [ -z "$RECORDED" ]; then
  echo "NOT COMPLETE: no checkpoint from activation." >&2
  echo "  Re-activate: labctl scenario up cluster-upgrade-drill --force" >&2
  exit 1
fi

REMAINING="$(unrolled_workers)"
recorded_n="$(printf '%s\n' $RECORDED | grep -c . || true)"
remaining_n="$(printf '%s\n' $REMAINING | grep -c . || true)"
if [ "${remaining_n:-0}" -ge "${recorded_n:-0}" ]; then
  echo "PENDING: no node has been rolled yet, so there is no upgrade window to grade." >&2
  echo "  Start load, then roll a worker:" >&2
  echo "    labctl traffic start --profile steady --rps 20" >&2
  exit 1
fi

STARTED="$(mark_roll_started)"
NOW="$(date -u +%s)"
WINDOW=$(( (NOW - STARTED) / 60 ))

# Do not look back further than Prometheus can see. This drill can destroy
# Prometheus's own volume — it is one of the things it teaches — and after that
# the window since the roll began mostly predates any data at all, so the rate
# averages down to nothing and the learner is failed for a loss the scenario
# told them to expect.
HAVE="$(promq "(time() - min(prometheus_tsdb_lowest_timestamp_seconds)) / 60")"
case "${HAVE:-}" in
  '' | *[!0-9.]*) ;;
  *)
    HAVE_MIN="${HAVE%%.*}"
    [ -n "$HAVE_MIN" ] && [ "$HAVE_MIN" -lt "$WINDOW" ] && WINDOW="$HAVE_MIN"
    ;;
esac

[ "$WINDOW" -lt 2 ] && WINDOW=2
[ "$WINDOW" -gt 30 ] && WINDOW=30

# Was anyone actually using the service while the node went away? A perfect
# success rate over no requests is not a result.
RPS="$(promq "sum(rate(${METRIC}_count{app=\"${WORKLOAD}\"}[${WINDOW}m])) or vector(0)")"
if [ -z "$RPS" ] || [ "$(awk -v a="${RPS:-0}" -v b="$MIN_RPS" 'BEGIN{print (a<b)}')" = "1" ]; then
  echo "FAIL: only ${RPS:-0} requests/s reached ${WORKLOAD} across the ${WINDOW}m upgrade window." >&2
  echo "  An upgrade nobody was using proves nothing about availability. Draining a node" >&2
  echo "  also evicts the k6 traffic Job, and its Job does not restart itself — so check" >&2
  echo "  and restart it after each roll, then let it run a couple of minutes:" >&2
  echo "    labctl traffic status" >&2
  echo "    labctl traffic start --profile steady --rps 20" >&2
  exit 1
fi

RATIO="$(promq "sum(rate(${METRIC}_count{app=\"${WORKLOAD}\",http_response_status_code!~\"5..\"}[${WINDOW}m])) / sum(rate(${METRIC}_count{app=\"${WORKLOAD}\"}[${WINDOW}m]))")"
if [ -z "$RATIO" ]; then
  echo "FAIL: Prometheus returned no availability figure for ${WORKLOAD}." >&2
  echo "  If Prometheus itself lost its volume to the roll, release the dead claims first:" >&2
  echo "    bash scenarios/cluster-upgrade-drill/scripts/reclaim-stranded-pvcs.sh" >&2
  exit 1
fi

if [ "$(awk -v a="$RATIO" -v b="$SLO" 'BEGIN{print (a<b)}')" = "1" ]; then
  echo "FAIL: ${WORKLOAD} served ${RATIO} of requests successfully across the ${WINDOW}m window, below the ${SLO} SLO." >&2
  echo "  The usual cause is draining while the PodDisruptionBudget still allowed the last" >&2
  echo "  healthy replica to go. Check the PDB was applied BEFORE the first drain, and that" >&2
  echo "  ${WORKLOAD} had somewhere else to run:" >&2
  echo "    kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get pdb -o wide" >&2
  echo "    kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get pods -o wide" >&2
  exit 1
fi

echo "OK: ${WORKLOAD} served ${RATIO} of requests successfully across the ${WINDOW}m since the"
echo "roll began, at ${RPS} req/s — above the ${SLO} SLO, measured on real load."
