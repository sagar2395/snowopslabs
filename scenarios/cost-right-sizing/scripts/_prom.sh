#!/usr/bin/env bash
# Shared helper: ask Prometheus for a scalar.
#
# The sizing floor comes from Prometheus rather than from `kubectl top` for two
# reasons. Right-sizing in production is done against an observed PEAK over a
# window, not a spot reading — a spot reading taken while the workload is idle
# blesses a request that will throttle the moment traffic returns. And
# metrics-server is briefly unavailable to a pod that has just rolled, which is
# exactly the moment the learner re-runs verify after right-sizing.

DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://prometheus.${DOMAIN_SUFFIX}}"
USAGE_WINDOW="${USAGE_WINDOW:-15m}"

# prom_scalar <query> — prints the first sample's value, or nothing.
# Never fails: callers decide what an absent value means, and a bare pipeline
# under `set -e` with pipefail would otherwise kill the script with no message.
prom_scalar() {
  curl -s -m 20 --get "${PROMETHEUS_URL}/api/v1/query" \
    --data-urlencode "query=$1" 2>/dev/null |
    sed -n 's/.*"value":\[[^,]*,"\([^"]*\)".*/\1/p' | head -1 || true
}
