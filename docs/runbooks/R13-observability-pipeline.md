# R13 — Observability pipeline: metrics, logs and traces

**Wave:** W4 · **Time:** ~30 minutes · **Cluster needed:** yes (k3d)

Proves the three signals actually flow end to end for `go-api`, and that each
one fails *loudly* rather than showing an empty panel. Every failure this
runbook checks for has happened: metrics for a route that was never
instrumented, logs deleted by enabling tracing, traces that never left the app,
a Grafana datasource on the wrong port, and PrometheusRules Prometheus never
read.

---

## Preconditions

- Docker/Colima running with ≥4 CPU / 8 GB.
- `bin/labctl` built (`make cli-build`).
- A cluster with the monitoring stack: `make init` then
  `labctl platform up monitoring/metrics monitoring/grafana`.
- `go-api` deployed: `labctl app deploy go-api`.
- Hostnames resolvable: `sudo labctl hosts add`.

---

## The gotchas, in one place

Every item here failed silently once. When a panel is empty, walk the pipeline
before you touch the query: **scrape target → label → export**.

**kube-prometheus-stack selectors.** `serviceMonitorSelector: {}` does *not*
mean "select everything". The chart treats an empty value as unset and falls
back to `*SelectorNilUsesHelmValues`, which restricts selection to
`release=<release>`. Set all three `*SelectorNilUsesHelmValues: false` flags,
and label every ServiceMonitor, PodMonitor and PrometheusRule
`release: prometheus` anyway.

**Tempo's HTTP API is port 3200**, not 3100. A datasource pointed at 3100 fails
every query and looks exactly like "nothing was traced".

**Kafka needs two metric sources.** `spec.kafka.metricsConfig` (JMX) gives
broker throughput and replication health; the separate kafka-exporter gives
consumer-group lag. Without `metricsConfig` there are no broker metrics at all.

**Every Strimzi pod carries `strimzi.io/kind=Kafka`, the exporter included.**
Select brokers on `strimzi.io/component-type: kafka` or you scrape the exporter
twice.

**Kafka's `messagesin` and `bytesin` exist as both per-topic and cluster-total
series.** Summing without `topic!=""` double-counts.

**Promtail labels come from relabelling.** `{app="go-api"}` only matches because
`promtail-values.yaml` relabels the pod's `app` label. No relabel, no label,
empty dashboard.

**The annotation scrape job is `kubernetes-pods` for every pod.** Dashboard
queries must filter on `app`, never on `job="<app name>"`.

**k6's remote-write metrics are in seconds**, not milliseconds, despite k6's own
CLI output being in ms. A panel labelled `ms` is wrong by 1000×.

**Exclude `/metrics`, `/health` and `/ready` from tracing** with
`otelhttp.WithFilter`. They run on a timer forever and bury the real traces.

**Apps export OTLP to Grafana Alloy, never straight to the backend.** Alloy
forwards to Tempo — see [ADR-0012](../adr/0012-alloy-as-trace-collector.md).

**Scenario checks should assert the metric exists**, so an empty dashboard fails
`labctl scenario verify` instead of quietly confusing a learner.

---

## §1 — Server-side metrics cover every route

The bug this catches: metrics recorded per handler, so a route added later is
served but never counted.

```bash
kubectl -n go-api exec deploy/go-api -- wget -qO- localhost:8080/ >/dev/null
kubectl -n go-api exec deploy/go-api -- wget -qO- localhost:8080/version >/dev/null
kubectl -n go-api exec deploy/go-api -- wget -qO- localhost:8080/metrics | grep '^http_requests_total'
```

**Expect:** a series for `path="/"` *and* `path="/version"`, each with
`app="go-api"`. A route that is served but missing here is the failure.

```bash
kubectl -n go-api exec deploy/go-api -- wget -qO- 'localhost:8080/nonsense-XYZ' >/dev/null 2>&1 || true
kubectl -n go-api exec deploy/go-api -- wget -qO- localhost:8080/metrics | grep 'path="other"'
```

**Expect:** `path="other"`. Unknown paths must collapse into one bucket — if the
raw path appears, any client can create unbounded series.

---

## §2 — Load generation, and both sides of it

```bash
labctl traffic start --profile steady --rps 25 --duration 15m
labctl traffic status
```

**Expect:** the k6 Job Running, and a line reporting either
`metrics: k6 -> Prometheus remote write (...)` or that no receiver was found.

Open **Grafana → Application Request Metrics** (`/d/app-requests`).

**Expect:** "Request rate by app" settles near 25 req/s within a minute, and on
"Offered vs served" the k6 line and the app line sit on top of each other. A
persistent gap means requests are being lost before reaching the app — that is a
finding, not noise.

> **Failure signature:** request rate flat at ~0.3 req/s. That is kubelet probe
> traffic only, meaning the load is hitting a route that is not metered, or the
> pod is not being scraped. Check
> `kubectl -n go-api get pod -o jsonpath='{.items[0].metadata.annotations}'`
> for `prometheus.io/scrape=true`.

---

## §3 — Logs reach Loki with the labels queries rely on

```bash
labctl scenario up observability-sre
kubectl -n go-api logs deploy/go-api --tail=5 | grep request
```

**Expect:** JSON lines with `method`, `path`, `status`, `durationMs`. Probe
endpoints (`/health`, `/ready`, `/metrics`) are excluded on purpose.

In **Grafana → Explore → Loki**:

```
{namespace="go-api"} | json
{app="go-api"}
```

**Expect:** both return lines. `{app="go-api"}` only works because Promtail
relabels the pod's `app` label — if the first query works and the second does
not, the relabel config is missing.

---

## §4 — Traces, through the collector

```bash
kubectl -n monitoring get deploy alloy
kubectl -n go-api get deploy go-api -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="OTEL_EXPORTER_OTLP_ENDPOINT")].value}'; echo
```

**Expect:** Alloy Ready 1/1, and the endpoint pointing at
`http://alloy.monitoring.svc.cluster.local:4318`. The app must export to the
**collector**, never to Tempo directly.

```bash
kubectl -n monitoring logs deploy/alloy --tail=20
```

**Expect:** no repeated exporter errors. Connection refused to `tempo:4317`
means Tempo is not up yet.

In **Grafana → Explore → Tempo**, search `service.name=go-api`.

**Expect:** traces, each with a server span and a child span.

> **Failure signature:** every Tempo query errors or returns nothing while Alloy
> logs look healthy. Check the datasource URL — Tempo's HTTP API is on **3200**.
> A datasource on 3100 fails identically to "nothing was traced".

---

## §5 — Correlation: log → trace → log

In **Explore → Loki**, run `{namespace="go-api"} | json`, expand one line.

**Expect:** a `trace_id` field with a **View trace** link that opens the trace in
Tempo. From that trace, the span links back to the pod's logs.

> **Failure signature:** logs stop entirely the moment tracing is enabled. That
> is the tracing middleware replacing the access-log middleware instead of
> wrapping it — `make test-apps` covers this
> (`TestAccessLog_LogsRequestsAndSkipsProbes`).

---

## §6 — Alerts are actually loaded

```bash
kubectl -n monitoring get prometheusrules
kubectl -n monitoring get prometheusrule scenario-observability-sre-alerts \
  -o jsonpath='{.metadata.labels.release}'; echo
```

**Expect:** the rule exists and its `release` label is `prometheus`.

Open **Prometheus → Alerts**.

**Expect:** the scenario's alerts are listed (Inactive is fine).

> **Failure signature:** the PrometheusRule exists but no alerts appear.
> kube-prometheus-stack only selects rules matching its `ruleSelector`. Confirm
> with:
> `kubectl -n monitoring get prometheus -o jsonpath='{.items[0].spec.ruleSelector}'`

Trip one:

```bash
kubectl -n go-api exec deploy/go-api -- wget -qO- localhost:8080/toggle-failure
```

**Expect:** `/ready` starts returning 503, pods leave Ready, and the readiness
alert moves to Pending then Firing. Toggle it back afterwards.

---

## §7 — Idempotency over an existing platform install

The regression this covers: re-running the scenario when Loki and Tempo are
already installed by the platform.

```bash
labctl platform up logging/loki tracing/tempo
labctl scenario up observability-sre
labctl scenario up observability-sre    # again
```

**Expect:** both runs succeed. The second reports
`loki: release already installed in monitoring — adopting it instead of
re-installing.`

> **Failure signature:** `UPGRADE FAILED: ... updates to statefulset spec for
> fields other than ... are forbidden`. That means a scenario is applying its own
> copy of the values instead of `platformValues:` (ADR-0010).

---

## §8 — Verify grades the pipeline, not just the pods

```bash
labctl scenario verify observability-sre
```

**Expect:** every check passes, including `app-exports-traces-to-collector` and
`app-request-metrics-present`. Now break one deliberately:

```bash
labctl traffic stop
bash scenarios/observability-sre/scripts/disable-tracing.sh
labctl scenario verify observability-sre
```

**Expect:** those two checks fail with remediation text naming the fix. A check
suite that stays green with tracing off and no traffic is not grading anything.

---

## §9 — Kafka metrics (event-driven-arch)

The bug this catches: a dashboard that renders with every panel empty because
nothing is being scraped.

```bash
labctl platform up data/kafka
labctl scenario up event-driven-arch
kubectl -n kafka get podmonitor
```

**Expect:** two PodMonitors, `kafka-exporter` and `kafka-broker`.

```bash
curl -s 'http://prometheus.k3d.local/api/v1/targets?state=active' \
  | grep -o 'podMonitor/kafka/[a-z-]*' | sort -u
```

**Expect:** both pools present and `up` — one target each. If `kafka-broker`
also lists the exporter pod, its selector is matching `strimzi.io/kind`, which
every Strimzi pod carries.

```bash
labctl scenario verify event-driven-arch
```

**Expect:** `broker-metrics-scraped` and `consumer-lag-metrics-scraped` pass.
They exist so an unscraped dashboard fails here rather than looking merely empty.

Open **Grafana → Kafka Overview** (`/d/kafka-overview`).

**Expect:** every panel populated. Broker panels (throughput, under-replicated
partitions, request latency) come from the JMX exporter configured by
`spec.kafka.metricsConfig`; lag and offset panels come from kafka-exporter.

> **Failure signature:** lag panels populated, broker panels empty. The Kafka CR
> is missing `spec.kafka.metricsConfig` — the exporter alone publishes no broker
> metrics.

### §9a — Upgrading Strimzi across a major version

`helm upgrade` never touches CRDs, and Strimzi 1.x serves `v1` only. Attempting
an in-place 0.x → 1.x upgrade must fail with an explanation, not a wall of CRD
errors:

```bash
labctl platform up data/kafka
```

**Expect (only on a cluster still running 0.x):** a message stating that Strimzi
cannot be upgraded in place, and naming the recovery. Follow it:

```bash
labctl platform down data/kafka
kubectl get crd | grep strimzi          # must return nothing
labctl platform up data/kafka
labctl scenario up event-driven-arch --force
```

**Expect:** no Strimzi CRDs survive the teardown, and the reinstall brings up
Kafka 4.3.1 on Strimzi 1.2.0.

> **Failure signature:** `platform down` reports success but `kubectl get crd`
> still lists ten `*.strimzi.io` CRDs. Neither Helm nor namespace deletion
> removes cluster-scoped CRDs; the uninstall script must do it explicitly.

---

## §10 — env-promotion drift detection

Uses the `env-promotion` scenario. The point is that the check grades live state,
not a bookkeeping record.

```bash
bash scenarios/env-promotion/scripts/check-images-consistent.sh   # expect exit 0
```

Then break it three ways, re-running the check after each:

```bash
# 1. Fake promotion: edit the record without rolling anything out.
kubectl -n env-staging patch cm env-metadata --type=merge -p '{"data":{"declared_tag":"v1.1.0"}}'

# 2. Start a rollout that can never converge.
kubectl -n env-staging set image deployment/go-api go-api=go-api:v9.9.9

# 3. Now make the record agree with the (wedged) spec.
kubectl -n env-staging patch cm env-metadata --type=merge -p '{"data":{"declared_tag":"v9.9.9"}}'
```

**Expect:** all three fail. The third is the important one — `declared_tag` and
the Deployment spec now *agree*, and only looking at the running pods reveals
that they are still serving the old image. A check that compared the record to
the spec alone would call this a success.

Restore:

```bash
kubectl -n env-staging set image deployment/go-api go-api=go-api:v1.0.0
kubectl -n env-staging patch cm env-metadata --type=merge -p '{"data":{"declared_tag":"v1.0.0"}}'
kubectl -n env-staging rollout status deployment/go-api
```

> **Failure signature:** the check reports pods running
> `docker.io/library/go-api:latest` when the Deployment correctly requested a
> version tag. That is a reporting artifact, not drift: when several tags share
> one digest in the node's image store, the kubelet's
> `.status.containerStatuses[].image` names whichever tag it resolved. Compare
> the pod's `.spec` image instead.

---

## Teardown

```bash
labctl traffic stop
labctl scenario down observability-sre
labctl lab down
```

`scenario down` must run `disable-tracing.sh`: confirm with
`kubectl -n go-api get deploy go-api -o yaml | grep OTEL` returning nothing.

It must also **leave the platform's releases installed** — Loki, Tempo and
Promtail are adopted, not owned:

```bash
helm list -n monitoring | grep -E 'loki|tempo|promtail'
```

**Expect:** all three still present. Only `alloy`, which the scenario installed
itself, is gone.

---

## Results

First run: k3d `snowops` (k3s v1.33.6), 2026-09-04.

| § | Step | Pass / Fail | Notes |
|---|---|---|---|
| 1 | Every route metered, unknown paths bucketed | **PASS** | `/`, `/version` metered; unknown → `path="other"`; `/metrics` not self-metered |
| 2 | k6 load visible client- and server-side | **PASS** | 25.3 req/s server vs 25.0 client. Found: k6 remote-write reports **seconds**, panel unit corrected |
| 3 | Logs in Loki with `app` and `namespace` labels | **PASS** | both `{namespace="go-api"}` and `{app="go-api"}` return data |
| 4 | Traces reach Tempo via Alloy | **PASS** | traces with `service.name=go-api`. Found: `/metrics` scrapes were being traced — now filtered |
| 5 | log → trace → log correlation | **PASS** | `trace_id` from a log line resolved to a trace with parent + child spans |
| 6 | Alert rules loaded and firing | **PASS** | 6 rules in 3 groups loaded (previously never loaded); `PodNotReady` + `HighErrorRate` fired on the injected failure |
| 7 | Scenario idempotent over platform install | **PASS** | second activation adopts loki/promtail/tempo, 16s, no Forbidden error. Found: platform installs needed the same recovery → `platform/_lib/helm.sh`; adopt must also skip teardown |
| 8 | Verify fails when the pipeline is broken | **PASS** | tracing off → 1 of 8 fails with runnable remediation. Found: remediation was not template-resolved; `alerting-rules-installed` was a tautology |
| 9 | Kafka broker + lag metrics scraped, dashboards populated | **PASS** | both PodMonitors up; all 14 Kafka Overview queries return data. Found: broker selector matched the exporter too |
| 9a | Strimzi major upgrade refuses in place and recovers via teardown | **PASS** | three separate walls found and handled — see ADR-0011 |
| 10 | env-promotion drift check catches all three failure modes | **PASS** | fake promotion, wedged rollout, and spec==declared-but-stale-pods all caught. Found: `.status.containerStatuses[].image` reports a tag alias when tags share a digest — the check uses pod `.spec` instead |

Upgrades exercised in place on a live cluster: loki 6.6.2 → 18.12.0 (PVC
preserved), tempo 1.10.3 → 2.3.0, grafana 10.5.15 → 13.2.0,
kube-prometheus-stack 88.6.3 → 89.2.0, promtail 6.16.4 → 6.17.1, strimzi
0.47.0 → 1.2.0 (via teardown), Kafka 3.9.0 → 4.3.1.
