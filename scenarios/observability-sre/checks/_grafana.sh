#!/usr/bin/env bash
# Shared helper: query a Grafana datasource through Grafana's own proxy.
#
# The proxy is deliberate. It is the path the learner uses in Explore, so a
# check that passes here proves the datasource the learner will actually click
# is wired correctly — not merely that a pod is listening. Probing Loki or
# Tempo directly would pass with a broken Grafana datasource, which is the most
# common way this stack looks "empty" to a learner.

MONITORING_NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
DOMAIN_SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"
GRAFANA_URL="${GRAFANA_URL:-http://grafana.${DOMAIN_SUFFIX}}"

# Read the admin password from the secret rather than assuming the default, so
# the check keeps working on a lab whose Grafana was installed with its own.
grafana_auth() {
  user="$(kubectl -n "$MONITORING_NAMESPACE" get secret grafana \
    -o jsonpath='{.data.admin-user}' 2>/dev/null | base64 -d 2>/dev/null)"
  pass="$(kubectl -n "$MONITORING_NAMESPACE" get secret grafana \
    -o jsonpath='{.data.admin-password}' 2>/dev/null | base64 -d 2>/dev/null)"
  printf '%s:%s' "${user:-admin}" "${pass:-admin}"
}

# ds_get <datasource-uid> <path> [curl args...]
ds_get() {
  uid="$1"
  path="$2"
  shift 2
  curl -s -m 25 -u "$(grafana_auth)" \
    "$@" "${GRAFANA_URL}/api/datasources/proxy/uid/${uid}${path}"
}

# The signals are graded over a recent window, not over all of history. Tempo
# and Loki retain what a PREVIOUS run of this scenario produced, so an unwindowed
# query is green the moment the lab has ever done this exercise — the learner's
# own work is never what makes it pass.
#
# Tempo filters at block granularity, so the window bounds staleness to roughly
# a quarter of an hour rather than to the second. That is the distinction worth
# having: it rules out a run from yesterday, not a span from ten minutes ago.
LOOKBACK_SECONDS="${LOOKBACK_SECONDS:-900}"
window_start() { expr "$(date +%s)" - "$LOOKBACK_SECONDS"; }
window_end() { date +%s; }
