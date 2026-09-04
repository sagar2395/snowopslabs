# Traffic

`labctl traffic` runs a k6 load generator in-cluster so scenarios, incidents and
autoscaling play out under realistic load. The generator lives in its own
`traffic` namespace; the scripts are in `src/services/traffic/`.

Never use a `curl` loop in scenario or incident content — use this.

## `labctl traffic start`

Starts the generator, replacing any active run.

```bash
labctl traffic start                                # steady 10 rps for 10m against go-api
labctl traffic start --profile spike --rps 20       # 20 rps baseline, 200 rps spike
labctl traffic start --profile soak --duration 4h
labctl traffic start --target http://echo-server.k3d.local/ --rps 50
```

| Flag | Default | Meaning |
|---|---|---|
| `--profile` | `steady` | one of the profiles below |
| `--rps` | `10` | requests per second; the baseline for `spike` |
| `--duration` | profile-specific | run length (`30s`, `10m`, `1h30m`). steady 10m, soak 2h, spike fixed |
| `--target` | go-api `/`, in-cluster | URL to load. Multi-endpoint profiles treat it as a base origin |
| `--method` | profile-specific | HTTP method for the `write` and `errors` profiles |

## Profiles

Discovered from `src/services/traffic/profiles/`.

| Profile | Shape |
|---|---|
| `steady` | constant rate, single endpoint |
| `spike` | 10× burst on a fixed ~6-minute shape, single endpoint |
| `soak` | long sustained run, single endpoint |
| `browse` | weighted read mix across `/`, `/version`, `/health` |
| `write` | write-heavy mix; the target is a base origin |
| `errors` | deliberately error-producing mix; the target is a base origin |

```bash
labctl traffic profiles
labctl traffic status      # active run, profile, pods and recent k6 output
labctl traffic stop        # stop and remove the job, configmap and namespace
```

## Why the default target is `/`

The app root is access-logged and metered, so a run is visible in
`kubectl logs` and in Grafana. Probe endpoints (`/health`, `/ready`,
`/metrics`) are deliberately excluded from the access log and from tracing —
they run on a timer forever and would bury real traffic.

## k6's own metrics

`start.sh` looks for the Prometheus service and, when it finds one,
remote-writes k6's client-side metrics — `k6_http_reqs_total`,
`k6_http_req_duration_p95`, `k6_http_req_failed_rate` — into it. These are the
other half of evaluating load: the app's `/metrics` only counts requests that
*arrived*, so a gap between "k6 sent" and "app handled" on the **Application
Request Metrics** dashboard means requests are being lost before the app sees
them.

Override the endpoint with `K6_PROMETHEUS_RW_SERVER_URL`, or set
`TRAFFIC_METRICS=off` to skip the probe.

> **k6's remote-write metrics are in seconds**, not milliseconds, even though
> k6's own CLI output is in ms. A panel labelled `ms` is wrong by 1000×.
> See [R13](../../runbooks/R13-observability-pipeline.md).
