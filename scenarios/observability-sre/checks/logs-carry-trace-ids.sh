#!/usr/bin/env bash
# Passes when the workload's logs are queryable in Loki AND carry the trace_id
# that links a log line to its span.
#
# That link is what makes the three signals one story rather than three
# dashboards. It is also the step that silently does not work: logs ship fine
# without tracing configured, and every line is then a dead end.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
. "$DIR/_grafana.sh"

NS="${WORKLOAD_NAMESPACE:-go-api}"
FROM="$(window_start)000000000"; TO="$(window_end)000000000"

RESULT="$(ds_get loki "/loki/api/v1/query_range" \
  --get --data-urlencode "query={namespace=\"${NS}\"} |= \`trace_id\`" \
  --data-urlencode "limit=5" \
  --data-urlencode "start=${FROM}" --data-urlencode "end=${TO}" 2>/dev/null || true)"

if [ -z "$RESULT" ] || ! printf '%s' "$RESULT" | grep -q '"status":"success"'; then
  echo "FAIL: Grafana's Loki datasource returned no successful response." >&2
  echo "  Is Promtail shipping? kubectl -n ${MONITORING_NAMESPACE} get ds promtail" >&2
  echo "  Is Loki up?           kubectl -n ${MONITORING_NAMESPACE} get sts loki" >&2
  exit 1
fi

if printf '%s' "$RESULT" | grep -q 'trace_id'; then
  echo "OK: ${NS} log lines from the last $((LOOKBACK_SECONDS / 60)) minutes are queryable in Loki and carry a trace_id."
  exit 0
fi

# Distinguish "no logs at all" from "logs without trace ids" — they have
# completely different causes and the learner should not have to guess which.
ANY="$(ds_get loki "/loki/api/v1/query_range" \
  --get --data-urlencode "query={namespace=\"${NS}\"}" \
  --data-urlencode "limit=1" \
  --data-urlencode "start=${FROM}" --data-urlencode "end=${TO}" 2>/dev/null || true)"

if printf '%s' "$ANY" | grep -q '"values"'; then
  echo "FAIL: ${NS} logs reach Loki, but no line carries a trace_id." >&2
  echo "  The app only stamps a trace_id once it is exporting spans, so wire it" >&2
  echo "  to the collector and send some traffic:" >&2
  echo "    kubectl -n ${NS} set env deployment/${WORKLOAD_NAME:-go-api} \\" >&2
  echo "      OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.${MONITORING_NAMESPACE}.svc.cluster.local:4318" >&2
  echo "    labctl traffic start --profile steady --rps 25" >&2
  exit 1
fi

echo "FAIL: no logs from namespace ${NS} are reaching Loki at all." >&2
echo "  Promtail relabels by namespace; confirm it is running on the node that" >&2
echo "  holds the pod: kubectl -n ${MONITORING_NAMESPACE} get pods -l app.kubernetes.io/name=promtail -o wide" >&2
exit 1
