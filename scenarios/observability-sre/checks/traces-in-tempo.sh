#!/usr/bin/env bash
# Passes when Tempo holds spans for the bound workload.
#
# This grades the OUTCOME of wiring the app to the collector, not the wiring.
# The environment variable can be set on a Deployment that never sends a span —
# a wrong endpoint, a collector that is down, an app with no traffic — and a
# check that reads the variable calls all of those a pass.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
. "$DIR/_grafana.sh"

SERVICE="${WORKLOAD_NAME:-go-api}"

FROM="$(window_start)"; TO="$(window_end)"
VALUES="$(ds_get tempo "/api/search/tag/service.name/values?start=${FROM}&end=${TO}" 2>/dev/null || true)"

if [ -z "$VALUES" ]; then
  echo "FAIL: Grafana's Tempo datasource returned nothing." >&2
  echo "  Tempo's HTTP API listens on 3200, not 3100 — a datasource pointing at" >&2
  echo "  the wrong port fails every query and looks like 'no traces recorded'." >&2
  echo "  Check it: Grafana > Connections > Data sources > Tempo." >&2
  exit 1
fi

if printf '%s' "$VALUES" | grep -q "\"${SERVICE}\""; then
  echo "OK: Tempo holds spans for service.name=${SERVICE} from the last $((LOOKBACK_SECONDS / 60)) minutes."
  exit 0
fi

echo "FAIL: Tempo has recorded no spans for service.name=${SERVICE} in the last $((LOOKBACK_SECONDS / 60)) minutes." >&2
echo "  Tempo knows about: ${VALUES}" >&2
echo "  Two things have to be true, in order:" >&2
echo "    1. ${SERVICE} exports to the collector —" >&2
echo "       kubectl -n ${WORKLOAD_NAMESPACE:-go-api} set env deployment/${SERVICE} \\" >&2
echo "         OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.${MONITORING_NAMESPACE}.svc.cluster.local:4318" >&2
echo "    2. Requests are arriving, so there is something to trace —" >&2
echo "       labctl traffic start --profile steady --rps 25" >&2
exit 1
