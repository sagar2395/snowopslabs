#!/usr/bin/env bash
set -eu

# Objective: re-run the experiment against the HARDENED service and prove the
# outage is gone.
#
# The measurement is the one the drill tells you to watch on the dashboard: the
# request rate never reaching zero. On one replica a pod kill takes the service
# down and the rate flatlines; on three, the survivors keep serving and it only
# dips. That difference is the whole point of the exercise, and it is the thing
# scaling out actually buys.
#
# Graded only once the workload is hardened AND an experiment has been run
# against it, so it cannot be satisfied by a quiet cluster nobody attacked.

NS="${WORKLOAD_NAMESPACE:-go-api}"
WORKLOAD="${WORKLOAD_NAME:-go-api}"
METRIC="${WORKLOAD_METRIC:-http_server_request_duration_seconds}"
PROM="${PROMETHEUS_URL:-http://prometheus.${DOMAIN_SUFFIX:-k3d.local}}"
WITNESS="chaos-experiment-witness"

promq() {
  curl -s --max-time 20 --get "${PROM}/api/v1/query" --data-urlencode "query=$1" |
    sed -n 's/.*"value":\[[0-9.]*,"\([^"]*\)"\].*/\1/p'
}

READY="$(kubectl -n "$NS" get deploy "$WORKLOAD" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)"
if [ "${READY:-0}" -lt 3 ]; then
  echo "PENDING: ${WORKLOAD} has ${READY:-0} ready replica(s), so a pod kill is still a full outage." >&2
  echo "  Harden it, then re-run the same experiment:" >&2
  echo "    kubectl -n ${NS} scale deploy/${WORKLOAD} --replicas=3" >&2
  exit 1
fi

# Remember when the workload first reached the hardened shape, so the window
# below starts there. Without that, the outage the learner correctly caused on
# one replica sits inside the window and fails the check they have just earned.
STATE="chaos-steady-state"
HARDENED="$(kubectl -n "$NS" get configmap "$STATE" -o jsonpath='{.data.hardened-at}' 2>/dev/null || true)"
NOW="$(date -u +%s)"
if [ -z "$HARDENED" ]; then
  HARDENED="$NOW"
  kubectl -n "$NS" patch configmap "$STATE" --type merge \
    -p "{\"data\":{\"hardened-at\":\"${NOW}\"}}" >/dev/null 2>&1 || true
fi

LAST_EXP="$(kubectl -n "$NS" get configmap "$WITNESS" -o jsonpath='{.data.last-seen}' 2>/dev/null || true)"
if [ -z "$LAST_EXP" ] || [ "$LAST_EXP" -lt "$HARDENED" ] 2>/dev/null; then
  echo "PENDING: ${WORKLOAD} is hardened, but no experiment has been run since." >&2
  echo "  The first attack proved the outage; this one proves it is gone. Repeat it now that" >&2
  echo "  the service has somewhere to fail over to:" >&2
  echo "    kubectl delete podchaos pod-kill-${WORKLOAD} -n ${NS} --ignore-not-found" >&2
  echo "    kubectl apply -f scenarios/chaos-engineering/manifests/chaos-experiments.yaml -l experiment=pod-kill" >&2
  exit 1
fi

# Was there load to measure at all? Averaged over the window, so a momentary dip
# does not read as "no traffic".
RATE="$(promq "sum(rate(${METRIC}_count{app=\"${WORKLOAD}\"}[10m])) or vector(0)")"
case "${RATE:-0}" in ''|0|0.*) LOW=1 ;; *) LOW=0 ;; esac
if [ "$LOW" = "1" ]; then
  echo "PENDING: Prometheus sees almost no requests to ${WORKLOAD} (${RATE:-0}/s)." >&2
  echo "  Without load the blast radius is unmeasurable — an experiment nobody was using proves nothing." >&2
  echo "    labctl traffic start --profile browse --rps 20 --duration 30m" >&2
  echo "  Let it run a couple of minutes, then re-run the experiment and re-verify." >&2
  exit 1
fi

# The grade: since hardening, the service never stopped serving. The window is
# measured from that moment — capped at 15 minutes so it stays a recent claim,
# floored at 3 so there is something to average over.
ELAPSED=$(( NOW - HARDENED ))
if [ "$ELAPSED" -lt 120 ]; then
  echo "PENDING: ${WORKLOAD} was hardened ${ELAPSED}s ago — too recent to measure." >&2
  echo "  Any window that short still contains the outage you correctly caused on one replica." >&2
  echo "  Re-run the experiment against the hardened service, give it a minute, and verify again." >&2
  exit 1
fi
# Never look back past the moment of hardening: the flatline on a single replica
# was the point of the first experiment, and counting it here would fail the
# learner for having done the drill properly.
WINDOW=$(( ELAPSED / 60 ))
[ "$WINDOW" -gt 15 ] && WINDOW=15
# The sub-interval matters more than the window. A pod kill takes the service
# out for roughly ten seconds; sampled once a minute that outage is averaged
# away and the floor never reaches zero, so the check would pass on a single
# replica and grade nothing.
FLOOR="$(promq "min_over_time((sum(rate(${METRIC}_count{app=\"${WORKLOAD}\"}[1m])) or vector(0))[${WINDOW}m:30s])")"
case "${FLOOR:-0}" in ''|0|0.0*) ZERO=1 ;; *) ZERO=0 ;; esac

if [ "$ZERO" = "1" ]; then
  echo "FAIL: ${WORKLOAD}'s request rate hit zero in the ${WINDOW} minute(s) since it was hardened." >&2
  echo "  The experiment still took the service out, so scaling out has not contained it." >&2
  echo "  Check every replica is actually Ready and spread across nodes before re-running:" >&2
  echo "    kubectl -n ${NS} get pods -o wide" >&2
  echo "  Make sure the load generator is still running — 'labctl traffic status' — before re-running." >&2
  exit 1
fi

echo "OK: ${READY} replicas serving, and the request rate never reached zero in the ${WINDOW} minute(s)"
echo "since hardening (floor ${FLOOR}/s, current ${RATE}/s) — the pod kill no longer takes the service down."
