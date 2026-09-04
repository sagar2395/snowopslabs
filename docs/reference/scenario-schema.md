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

## Template variables

URLs, commands, namespaces, snippets and manifests are Go templates.

| Variable | Example | Meaning |
|---|---|---|
| `{{.DomainSuffix}}` | `k3d.local` | Ingress domain suffix from the active runtime |
| `{{.MonitoringNamespace}}` | `monitoring` | Where the monitoring stack lives |
| `{{.ProjectRoot}}` | `/path/to/project` | Absolute path to the content root |

An unknown variable is a validation error, not an empty string.

## References and snippets

Two optional blocks that turn a scenario into a jumping-off point. Both are
shown by `labctl scenario info` and are template-resolved, so they display with
the deployment's real namespaces and domains.

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
  - label: "Helm values, not a kubectl manifest"
    path: values/overprovisioned.yaml
    apply: "helm upgrade -f -"
```

- A reference needs a `label` and an `http(s)` `url`; `note` is optional.
- A snippet needs a `label` and **exactly one** of `yaml` (inline text) or
  `path` (a file in the scenario directory). `labctl validate` fails on a `path`
  that does not resolve, naming the file and the snippet.
- `apply` overrides the per-snippet "apply with" hint, which defaults to
  `kubectl apply -f -`. Set it when the snippet is not a kubectl manifest, so
  the learner is not told to apply something that is not appliable.

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
