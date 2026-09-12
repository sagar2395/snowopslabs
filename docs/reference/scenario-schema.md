# Scenario schema

The complete `scenario.yaml` reference. A scenario is a directory under
`scenarios/` containing this file; the engine auto-discovers any directory with
a valid one. Schema validation runs in CI, so a malformed scenario cannot merge.

For a guided walkthrough instead of a reference, read
[your first scenario](../authoring/first-scenario.md).

## Skeleton

```yaml
apiVersion: scenario.snowops.net/v2   # optional; defaults to v2
name: my-scenario                     # must match the directory name
displayName: "My Scenario"            # shown in the UI and CLI
description: "What this scenario teaches"
category: observability               # grouping label

prerequisites:
  platform: [ingress, monitoring/metrics]
  apps: [go-api]
  capabilities: [prometheus-metrics]   # what the bound app must promise

runtimes: [k3d, kind]                 # optional; compatible runtimes

objectives:                           # human-readable goals
  - "Aggregate application logs in Loki"
  - "Keep p99 latency under 300ms"

stages:                               # ordered groups of components
  - name: baseline
    description: Install the baseline stack
    components: [...]

checks: [...]                         # machine-verifiable assertions

explore:
  urls:
    - label: "My Dashboard"
      url: "http://my-app.{{.DomainSuffix}}"
  commands:
    - label: "Check status"
      command: "kubectl get pods -n my-ns"
  tips:
    - "Generate traffic first, or the dashboards stay empty"
```

## Components

A component is one installable thing. Use **either** a top-level `components:`
list **or** `stages:` — never both. Stages are preferred: they group components
into phases you can reason about (`baseline`, `inject-failure`) and install in
declaration order.

```yaml
components:
  - name: loki
    type: helm                        # helm | manifest | grafana-dashboard | script
    chart: grafana-community/loki
    repo: https://grafana-community.github.io/helm-charts
    version: "18.12.0"                # always pinned — see config/versions.env
    namespace: "{{.MonitoringNamespace}}"
    platformValues: logging/loki      # base values, owned by the platform
    valuesFile: values/loki.yaml      # overlay, applied on top
    adopt: true                       # reuse an existing release, do not upgrade
    set:
      key: value                      # --set overrides
```

| Field | Applies to | Meaning |
|---|---|---|
| `name` | all | Unique, non-empty within the scenario |
| `type` | all | `helm`, `manifest`, `grafana-dashboard` or `script` |
| `chart` / `repo` / `version` | `helm` | Chart, repo URL, and the **required** pinned version |
| `namespace` | all | Target namespace; template-resolved |
| `platformValues` | `helm` | Platform component whose values are the base — see below |
| `valuesFile` | `helm` | Values file relative to the scenario directory, layered on top |
| `set` | `helm` | Map of `--set` key/value overrides |
| `adopt` | `helm` | Reuse an already-installed release instead of upgrading it |
| `path` | `manifest`, `grafana-dashboard` | YAML file, or a directory of dashboard JSON |
| `script` | `script` | Shell script relative to the scenario directory |
| `uninstallScript` | `script` | Reverses that script on `scenario down` |

**Component types**

| Type | What it does |
|---|---|
| `helm` | Adds the repo and installs the chart at the pinned version with the resolved values |
| `manifest` | `kubectl apply` of the YAML at `path` |
| `grafana-dashboard` | Creates a ConfigMap from the dashboard JSON at `path`, which the Grafana sidecar picks up |
| `script` | Runs the shell script at `script` |

### Dashboards — an App selector, and legends a reader can follow

A dashboard that shows the workload declares an `app` variable, plus a
`namespace` variable when its queries need one, and its queries use `$app` and
`$namespace` rather than `{{.WorkloadName}}`. The variables default to the
activation's app, and the reader can switch to any other:

```json
{
  "name": "app", "type": "query",
  "query": {"query": "query_result(count by (app) ({{.WorkloadMetric}}_count) or label_replace(vector(1), \"app\", \"{{.WorkloadName}}\", \"\", \"\"))"},
  "regex": "/app=\"([^\"]+)\"/",
  "current": {"text": "{{.WorkloadName}}", "value": "{{.WorkloadName}}"}
},
{
  "name": "namespace", "type": "query",
  "query": {"query": "label_values(kube_deployment_spec_replicas{deployment=\"$app\"}, namespace)"},
  "current": {"text": "{{.WorkloadNamespace}}", "value": "{{.WorkloadNamespace}}"}
}
```

The `or label_replace(vector(1), …)` keeps the activation's app in the list even
when it exposes no request metric. The dashboard `uid` and `title` stay fixed.

`labctl validate` lints every dashboard's legends:

- A query grouped `by (label)` must name one of those labels in its legend,
  e.g. `{{namespace}}: available`. Otherwise every line carries the same name and
  nobody can tell the environments apart. A label pinned with `label="value"`
  yields one series and needs no legend label.
- A legend may only name labels the query returns. `{{code}}` on a query grouped
  by `http_response_status_code` renders empty.

Queries using `without`, `label_replace` or `label_join` are not judged, and
neither are tables.

### `platformValues` — one values file per component

A component's Helm values live **once**, at
`platform/<category>/<component>/values.yaml`. A scenario names that file with
`platformValues:` and supplies only its differences in `valuesFile:`. It never
keeps a second full copy.

```yaml
platformValues: logging/loki                        # the whole component's values
platformValues: logging/loki/promtail-values.yaml   # or one specific file
valuesFile: values/loki.yaml                        # overlay
```

Duplicated values drift, and Helm reports the drift as a forbidden update to an
immutable StatefulSet field. See
[ADR-0010](../adr/0010-platform-values-single-source.md).

### `adopt` — do not clobber a platform release

`adopt: true` reuses an already-installed release rather than upgrading it, so a
scenario never overwrites something the platform owns. A scenario that adopts a
platform release must not uninstall it on teardown.

### `uninstallScript` — undo a script component

Without it, a script's side effects — an env var set on a Deployment, say —
outlive the scenario that created them.

```yaml
- name: enable-tracing
  type: script
  script: scripts/enable-tracing.sh
  uninstallScript: scripts/disable-tracing.sh
```

## Checks

Checks are the grading primitive. Write them as "what must be true when this
scenario is healthy". They run in declaration order, and a failure does not stop
the rest.

```yaml
checks:
  - name: loki-ready
    type: kubectl                     # http | kubectl | promql | script
    resource: statefulset/loki        # type/name, or a bare type for existence
    namespace: "{{.MonitoringNamespace}}"
    jsonpath: "{.status.readyReplicas}"
    operator: ">="                    # == != < <= > >= contains
    value: "1"

  - name: grafana-reachable
    type: http
    url: "http://grafana.{{.DomainSuffix}}"
    expectStatus: 200                 # default 200
    bodyContains: "Grafana"           # optional

  - name: latency-ok
    type: promql                      # queries $PROMETHEUS_URL, default
    query: 'histogram_quantile(...)'  # http://prometheus.<DOMAIN_SUFFIX>
    operator: "<"
    value: "0.3"

  - name: custom
    type: script                      # exit 0 passes; runs with DOMAIN_SUFFIX,
    script: checks/custom.sh          # MONITORING_NAMESPACE and PROJECT_ROOT set
    timeoutSeconds: 60                # any check may override the 30s default
    remediation: "run the fix: …"     # shown under a failing check
    pending: true                     # render PENDING rather than FAIL
```

Each check type accepts only its own fields — an `http` check carrying a `query`
is rejected, not ignored.

### `remediation` and `pending`

These make `verify` teach instead of alarm.

- A failing check with a `remediation` prints it under **Next step(s)**. The
  generic "a pod may still be starting" hint appears only for a failure with no
  remediation of its own.
- A failing `pending` check renders **PENDING** and reads as an incomplete drill
  step, so "you have not run the backup yet" never looks like "the scenario is
  broken". `verify` still exits non-zero.

Assert on the observability pipeline too. A check that the metric *exists* means
an empty dashboard fails `verify` instead of quietly confusing a learner — see
[R13](../runbooks/R13-observability-pipeline.md).

> When asserting on a running image, compare `.spec.containers[].image`, not
> `.status.containerStatuses[].image`. The latter reports whichever tag the
> kubelet resolved, so it names `:latest` for a pod that requested `:v1.1.0`
> when both tags share a digest.

## Parameters

Tunable knobs exposed at activation time and substituted into the scenario's
manifests as `{{.Name}}`. No parameters, or no overrides, means unchanged
behaviour.

```yaml
parameters:
  - name: MaxReplicas
    displayName: "Maximum replicas"
    description: "Ceiling the autoscaler will not exceed."
    default: "6"                      # required
    type: int                         # int | string (default string)
    min: 1                            # inclusive bounds, int only
    max: 10
  - name: MinReplicas
    default: "1"
    type: int
    min: 1
    max: 10
    notGreaterThan: MaxReplicas       # enforced against the effective values
```

Override at activation:

```bash
labctl scenario up autoscaling-under-load --set MaxReplicas=4 --set Threshold=15
```

An `int` parameter is bounds-checked and parsed before substitution, for both
the default and any override.

## Prerequisites

| Field | Meaning |
|---|---|
| `platform` | Platform components that must be installed |
| `apps` | Apps whose `apps/<name>/app.env` must exist |
| `capabilities` | What the **bound workload** must declare |

### `apps` is for a pinned app, not for the one you are bound to

Write `apps: ["{{.WorkloadName}}"]` and the entry means "the bound workload must
exist" — not "this scenario requires go-api". Every shipped scenario is written
this way, and the UI treats the two differently: a templated entry is shown as
**Runs against**, with a picker offering every app in the lab; a literal one is
shown as a requirement the user has to satisfy, and it disables the picker.

Prefer `capabilities` over a literal app. Pinning one is a restriction that
cannot be lifted at activation time.

### `capabilities` — stating what you need, not who provides it

A scenario that names an app can only ever run against that app. One that states
what it needs of an app can run against any app that provides it — including a
user's own. See [ADR-0014](../adr/0014-workload-binding-and-app-contract.md).

```yaml
prerequisites:
  capabilities:
    - prometheus-metrics
    - otlp-tracing
```

The vocabulary is closed — `labctl app capabilities` lists it, and an unknown
name fails `labctl validate` rather than silently meaning "never satisfied":

| Capability | The app… |
|---|---|
| `prometheus-metrics` | serves the metrics path in Prometheus format, including the request-duration histogram |
| `otlp-tracing` | honours `OTEL_EXPORTER_OTLP_ENDPOINT` and exports spans |
| `readiness-toggle` | can have its readiness flipped to failing on demand |

Require only what the scenario genuinely uses. A requirement is a restriction on
which apps can run it, so an unnecessary one narrows the scenario for nothing.

Preflight refuses to activate a scenario the bound app cannot satisfy, before
anything installs. `labctl scenario info` shows the requirement graded against
the app currently bound:

```
Prerequisites (workload capabilities), bound to "echo-server":
  - prometheus-metrics     ok
  - otlp-tracing           missing
  - readiness-toggle       missing
```

## Template variables

URLs, commands, namespaces, snippets and manifests are Go templates.

| Variable | Example | Meaning |
|---|---|---|
| `{{.DomainSuffix}}` | `k3d.local` | Ingress domain suffix from the active runtime |
| `{{.MonitoringNamespace}}` | `monitoring` | Where the monitoring stack lives |
| `{{.ProjectRoot}}` | `/path/to/project` | Absolute path to the content root |
| `{{.LokiRetentionPeriod}}` | `72h` | Loki's configured retention |
| `{{.IngressClass}}` | `traefik` | Ingress class for scenario Ingress manifests |
| `{{.WorkloadName}}` | `go-api` | The bound app's name, and its Deployment name |
| `{{.WorkloadNamespace}}` | `go-api` | Where the bound app is deployed |
| `{{.WorkloadService}}` | `go-api.go-api.svc.cluster.local` | Its in-cluster DNS name |
| `{{.WorkloadPort}}` | `8080` | The port it serves HTTP on |
| `{{.WorkloadMetric}}` | `http_server_request_duration_seconds` | The request-duration histogram it exposes |
| `{{.SinceActivation}}` | `95m` | Time since the scenario was activated (or the incident injected), as a PromQL range; `1m` when nothing is active |

An unknown variable is a validation error, not an empty string.

### Grading something that already happened

A check for an event — an alert fired, a backlog built up — looks back over a
range. Use `{{.SinceActivation}}` for it, not a fixed window:

```yaml
query: 'max_over_time((count(ALERTS{scenario="my-scenario",alertstate="firing"}) or vector(0))[{{.SinceActivation}}:1m])'
```

A fixed `[15m:1m]` fails a learner who did the drill early and verified later.
An unbounded one counts the previous run's alerts. The range opens when
`scenario up` finishes, and re-activating opens a new one.

### The workload variables

A scenario names the app it acts on through `{{.Workload*}}` rather than a
literal, so the same scenario runs against a built-in app or one the user brings
(see [ADR-0014](../adr/0014-workload-binding-and-app-contract.md)):

```yaml
checks:
  - name: app-scaled-up
    type: kubectl
    resource: deployment/{{.WorkloadName}}
    namespace: "{{.WorkloadNamespace}}"
    jsonpath: "{.status.readyReplicas}"
    operator: ">="
    value: "3"

  - name: latency-within-slo
    type: promql
    query: 'histogram_quantile(0.99, sum(rate({{.WorkloadMetric}}_bucket[5m])) by (le))'
    operator: "<"
    value: "1.5"
```

`{{.WorkloadMetric}}` names the histogram rather than assuming it, because each
language's instrumentation library picks its own. Derive a request rate from its
`_count` series (`rate({{.WorkloadMetric}}_count[5m])`) — the OpenTelemetry
semantic conventions the app contract mandates define no separate counter.

The names are flat (`{{.WorkloadName}}`, not `{{.Workload.Name}}`). The expander
matches a single dotted identifier on purpose, so it never rewrites the Helm,
Prometheus and Grafana templating that shares these files; a nested reference
would pass through unresolved and reach kubectl verbatim.

### Templates in content, environment variables in scripts

The engine resolves `{{.Workload*}}` in everything it reads itself — `scenario.yaml`,
the manifests it applies, dashboard JSON, snippets and prose. It does **not**
resolve a script: a script is executed as a file, so a `{{.WorkloadName}}` inside
one reaches `kubectl` verbatim.

Scripts get the binding as flags or from the environment. Every component
script, check script and fault script run by the engine is given:

| Variable | Example |
|---|---|
| `WORKLOAD_NAME` | `go-api` |
| `WORKLOAD_NAMESPACE` | `go-api` |
| `WORKLOAD_PORT` | `8080` |
| `WORKLOAD_METRIC` | `http_server_request_duration_seconds` |

A learner also runs scripts by hand, from a terminal that has none of these. So
a scenario script sources the shared helper first, which takes the binding from
`--app` and `--namespace` flags and removes them from `"$@"`:

```sh
#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

kubectl -n "$WORKLOAD_NAMESPACE" get deploy "$WORKLOAD_NAME"
```

Flags win over the environment, and `--app` without `--namespace` means a
namespace of the same name. With neither, the script stops with a usage error
rather than guessing an app. Never write a `:-go-api` fallback: a script run by
hand for echo-server would quietly act on go-api.

Every command that calls such a script passes both flags, so the copied text is
already filled in for the activation's app:

```yaml
explore:
  commands:
    - label: "Build the new version"
      command: "bash scenarios/env-promotion/scripts/build-image.sh v1.1.0 --app {{.WorkloadName}} --namespace {{.WorkloadNamespace}}"
```

`labctl validate` fails an explore command, check remediation, tip or snippet
`apply` that calls a script sourcing the helper without `--app`.

A manifest the learner applies by hand goes through `labctl scenario render`,
never `kubectl apply -f` on the file itself, which would send the literal
`{{.WorkloadName}}` to the cluster. `labctl validate` fails the second form when
the file contains a template variable:

```yaml
      command: "labctl scenario render node-drain-drill manifests/baseline.yaml --app {{.WorkloadName}} | kubectl apply -f -"
```
 Apply the same
rule to traffic: write `labctl traffic start --app {{.WorkloadName}}`.

> Incident scripts additionally receive `TARGET_NAMESPACE` and `TARGET_WORKLOAD`
> from the fault's own `target:` block, which is what a fault pinned to one
> application uses. See [incidents/README.md](../../incidents/README.md).

### What not to template

- **File paths.** `path: manifests/{{.WorkloadName}}-tls.yaml` looks for a file
  that does not exist. Name the file for its role instead.
- **Check names.** They are recorded against scores, so a templated name makes a
  result history incomparable. Name the check for what it asserts —
  `workload-healthy`, not `go-api-healthy`.
- **Dashboard UIDs**, for the same reason: they are stable identities.

### Writing a check that does not grade the language

A threshold calibrated against one application grades that application's runtime,
not the engineer's work. A p99 bound a Go service clears easily is one a JVM
fails on warmup alone — and a user's own application has no calibration at all,
so it would fail a check it has no way to satisfy.

Per-application baselines do not fix this: a conforming app still arrives without
one, and a *declared* baseline can simply be set generously enough to pass. Write
the assertion so it needs no calibration instead.

| Instead of | Assert | Why it travels |
|---|---|---|
| `p99 < 1.5` | `p99 / p50 < N` | Tail amplification is the same question for a 2ms service and a 200ms one — it measures degradation, not speed |
| `readyReplicas >= 3` | `readyReplicas > {{.MinReplicas}}` | "It scaled" is the lesson; a fixed count encodes one runtime's throughput per replica |
| a latency bound | error ratio `< 0.01` | Saturation shows up as errors in every runtime |

Most checks need no thought here: `readyReplicas >= 1`, `deployment exists`, and
`the metric is being scraped` already grade configuration rather than speed.

**When an absolute number really is the lesson** — an availability SLO in a
drill — make it a parameter so it reads as a deliberate choice, and say in its
description what a failure means:

```yaml
parameters:
  - name: AvailabilitySLO
    displayName: "Availability SLO across the drain"
    description: "Lower it if your workload is slower to become Ready than the
      drill allows — a slow-starting runtime failing this is a real finding about
      replica count and readiness gating, not a defect in the application."
    default: "0.995"
    type: string

checks:
  - name: availability-held-during-drain
    type: promql
    query: '...'
    operator: ">="
    value: "{{.AvailabilitySLO}}"
```

A check is graded against the values the scenario was **activated** with, so
`--set AvailabilitySLO=0.99` changes what `verify` requires. Parameters declared
by the scenario are legal template variables anywhere in it, and `labctl validate`
rejects a reference to one that is not declared.

Everything `labctl scenario info` and the UI display is resolved before it is
shown — component namespaces and charts included — using the scenario's
parameter **defaults** where a `{{.Param}}` appears. A learner therefore reads
`ns=monitoring` and `minReplicaCount: 1`, never the raw placeholder, and both
surfaces render the same content identically.

Templates belonging to another system are left alone: Prometheus rule
annotations (`{{ $value }}`, `{{ $labels.pod }}`), Loki `line_format`, and
`kubectl -o go-template` expressions print literally, because resolving them
would corrupt the very thing the snippet is teaching.

## References and snippets

Two optional blocks that turn a scenario into a jumping-off point. Both are
shown by `labctl scenario info` and the UI's Implementation tab, template-resolved
for the app the scenario runs against, so they display with its real names,
namespaces and domains.

```yaml
references:
  - label: "KEDA — ScaledObject specification"
    url: "https://keda.sh/docs/latest/reference/scaledobject-spec/"
    note: "Optional one-liner on why this link is relevant."

snippets:
  - label: "KEDA ScaledObject for go-api"
    description: "Optional context shown above the manifest."
    path: manifests/scaledobject.yaml
  - label: "A quick inline manifest"
    yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: demo
        namespace: "{{.MonitoringNamespace}}"
  - label: "The autoscaler you write"
    path: manifests/scaledobject.yaml
    exercise: true
  - label: "Wiring the app to the collector"
    path: scripts/enable-tracing.sh
    exercise: true
    apply: "bash scenarios/observability-sre/scripts/enable-tracing.sh --app {{.WorkloadName}} --namespace {{.WorkloadNamespace}}"
```

- A reference needs a `label` and an `http(s)` `url`; `note` is optional.
- A snippet needs a `label` and **exactly one** of `yaml` (inline text) or
  `path` (a file in the scenario directory). `labctl validate` fails on a `path`
  that does not resolve, naming the file and the snippet.
- `exercise: true` marks a snippet nothing installs, because applying it is the
  learner's task. The UI badges it **You apply this** and offers a ready-to-run
  command. Say what it does in `description`, not "apply this yourself".
- `apply` is the complete command for an exercise. Leave it empty for a manifest:
  the command becomes the manifest piped into `kubectl apply -f -`. Set it for
  anything else, such as a script.
- `description` is shown above the body, and is where the explanation belongs.
  The body is trimmed for reading: the file's leading comment banner and every
  block of two or more comment lines are dropped, while single-line comments,
  comments after code, heredoc bodies and YAML block scalars stay.

## `verified`

```yaml
verified: true    # confirmed end-to-end on a fresh cluster
```

Curation metadata, not a user-facing badge. It records which content has been
confirmed to activate, pass its checks and tear down cleanly on a fresh cluster,
so the nightly e2e job knows what to guard. It is deliberately not surfaced in
the CLI or UI: shipped content is expected to work. Absent or `false` means "not
yet confirmed".

## Validation rules

Enforced at load time — an invalid scenario refuses to load, and CI fails on it.

- `components` or `stages`, never both.
- Stage, component and check names are unique and non-empty.
- Every `helm` component has a pinned `version`.
- Each check type accepts only its own fields.
- Asset paths stay inside the scenario directory; absolute paths and `..`
  traversal are rejected.
- Template variables must be known.
- A `path` on a snippet or component must resolve.
- A command that calls a workload-bound script passes `--app`.
- A command never runs `kubectl apply|create|replace|delete -f` on a templated
  scenario file; it pipes `labctl scenario render` instead.
- A dashboard legend names the labels its query groups by, and only labels the
  query returns.

```bash
labctl validate            # everything: scenarios, incidents, paths, challenges
labctl validate --json
```

## Sharing scenarios

A scenario is just a directory, so sharing one is git and nothing else — no pack
format, registry or publish step ([ADR-0008](../adr/0008-content-extensibility-seam.md)).

```bash
git clone https://github.com/org/our-scenarios ~/our-scenarios
export SNOWOPS_CONTENT_PATH=~/our-scenarios
labctl scenario list        # yours appear, badged as external
```

`SNOWOPS_CONTENT_PATH` accepts several roots separated by the OS path
separator.

- Each root is scanned for directories containing a `scenario.yaml`, and every
  one is schema-validated. An invalid scenario is reported by name and skipped;
  it never hides the rest.
- External scenarios work with `up`, `down`, `verify` and `info` exactly like
  in-repo ones, and show their root in the SOURCE column of `scenario list`.
- **In-repo scenarios win name collisions.** A colliding external scenario is
  skipped, with the conflict named.

> **Security.** A scenario's components run scripts and apply manifests on your
> cluster with your credentials. Only point `SNOWOPS_CONTENT_PATH` at sources
> you trust, and read them first. This is the same trust level as running any
> script from that repository.

## Creating one

```bash
labctl scenario new my-scenario       # scaffolds a valid, verify-green scenario
labctl validate                       # check it
labctl scenario up my-scenario
labctl scenario verify my-scenario
```

Add supporting files under `values/`, `manifests/`, `dashboards/` and `checks/`
as needed.
