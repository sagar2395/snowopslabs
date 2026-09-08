# Applications & services

## `labctl app`

Applications live in `apps/<name>/`, each with an `app.env` declaring its
`BUILD_STRATEGY`, `DEPLOY_STRATEGY` and `HELM_VALUES`. The CLI reads that file
and runs the matching strategy — it never hardcodes how an app is built.

```bash
labctl app list              # discovered apps, their strategies and capabilities
labctl app build go-api      # build the container image
labctl app deploy go-api     # deploy to the cluster
labctl app destroy go-api    # remove it
labctl app capabilities      # the vocabulary a scenario may require
labctl app verify go-api     # check the app against the contract it declares
```

REST: `GET /api/v2/apps`, `GET /api/v2/apps/{name}/detail`,
`POST /api/v2/apps/{name}/{build,deploy,destroy}`.

## `labctl app verify` — the workload contract

An app declares a **contract** in its `app.env`: the interface it promises a
scenario, so a scenario can name the contract instead of naming the app. See
[ADR-0014](../../adr/0014-workload-binding-and-app-contract.md).

```bash
APP_PORT=8080
APP_HEALTH_PATH=/health          # optional, this is the default
APP_READY_PATH=/ready            # optional, this is the default
APP_METRICS_PATH=/metrics        # optional, this is the default
APP_REQUEST_METRIC=http_server_request_duration_seconds
APP_CAPABILITIES=prometheus-metrics,otlp-tracing,readiness-toggle
```

Only `APP_CAPABILITIES` has no default. The base interface is assumed; a
capability is claimed explicitly or the app does not have it.

`APP_REQUEST_METRIC` names the request-duration histogram rather than assuming
one, because each language's instrumentation library picks its own name.

`verify` runs in two phases:

| Phase | Needs a cluster | What it does |
|---|---|---|
| Declaration | no | Parses the contract and reports it. `--static` stops here. |
| Evidence | yes | Runs the checks each claim implies against the deployed workload. |

An app is only checked for what it claims, so it is never failed for lacking a
capability it never advertised. Two capabilities are reported as **declared, not
machine-checked**: `otlp-tracing` (the app exports only once
`OTEL_EXPORTER_OTLP_ENDPOINT` is set, which a tracing scenario does) and
`readiness-toggle` (proving it means deliberately making the app unready). Both
are conditional promises — a baseline deployment looks identical whether or not
the app honours them, so failing an app for one would fail a conforming app.

```
$ labctl app verify go-api
PASS  workload-ready          (kubectl)  got: 3, want: >= 1
PASS  serves-contract-port    (kubectl)  got: 8080, want: contains 8080
PASS  liveness-probe-path     (kubectl)  got: /health, want: == /health
PASS  readiness-probe-path    (kubectl)  got: /ready, want: == /ready
PASS  request-metric-scraped  (promql)   got: 8, want: >= 1
```

The metric check queries Prometheus rather than the pod, so it proves both that
the app exposes the histogram and that scraping works — the state every
metric-driven scenario actually depends on.

## Binding a scenario to an app

`APP_NAME` selects the workload every scenario and fault is bound to. Content
resolves it through `{{.WorkloadName}}` and friends
([scenario schema](../scenario-schema.md#the-workload-variables)), and the port
and metric come from that app's contract rather than a compiled-in guess.

```bash
labctl scenario info observability-sre               # bound to the default app
APP_NAME=echo-server labctl scenario info observability-sre
```

A scenario states what it needs of the app in
`prerequisites.capabilities`. Preflight refuses to activate one the bound app
cannot satisfy, before anything installs:

```
preflight failed for scenario "observability-sre":
  - workload "echo-server" does not declare: otlp-tracing, readiness-toggle —
    either bind an app that does (APP_NAME=<app>, see 'labctl app list') or add
    the capability to apps/echo-server/app.env once the app truly provides it
```

## `labctl service`

Shared services that apps depend on — Redis and friends. They live outside the
platform categories because they are application dependencies, not platform
capability.

```bash
labctl service list             # available shared services
labctl service up redis         # install
labctl service down redis       # uninstall
labctl service status           # all services
labctl service status redis     # one service
```
