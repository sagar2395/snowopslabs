# ADR 0014 — Workload binding and the app contract

**Status:** Proposed
**Date:** 2026-09-08

## Context

Scenarios and incidents name a concrete application. Of 789 app references
across `scenarios/`, `incidents/`, `challenges/` and `learn/`, 758 are `go-api`;
`echo-server` appears only in `oom-kill`, the challenge built on it, and one
stray line in `cluster-upgrade-drill/scripts/roll-node.sh`. The apparent
"some scenarios need go-api, some need echo-server" split is a single outlier,
not a design.

The real limitation is different. A platform-engineering simulator should let a
user run a scenario against **their own** application, and should let them
compare how different stacks behave under the same fault. Today neither is
possible: the app is baked into check literals, manifest names, PromQL selectors
and prose.

Two mechanisms already point the right way.

- **Incidents are half-decoupled already.** `fault.yaml` carries
  `target: {namespace, workload}`, `incident.go` exports it as
  `TARGET_NAMESPACE` / `TARGET_WORKLOAD`, and every inject/resolve/check script
  reads it with the app name only as a shell default.
- **Scenarios have a typed template context.** `catalog.TemplateContext`
  resolves `{{.MonitoringNamespace}}` and `{{.DomainSuffix}}` under
  `missingkey=error`, so an unknown key fails at validation.

The capability surface content actually depends on is small: HTTP probes on a
known port, RED metrics, an OTLP endpoint env var, and one readiness toggle.

## Decision

Scenarios depend on a **role**, not an app. The lab holds a **binding** from
that role to a concrete workload — `go-api` by default, swappable to another
built-in app or one the user brings.

**1. One role for now: `http-service`.** Twelve of thirteen scenarios use
exactly this shape. Additional roles are added when a scenario needs one, never
speculatively.

**2. Five flat `{{.Workload*}}` variables join the template context:**
`WorkloadName`, `WorkloadNamespace`, `WorkloadService`, `WorkloadPort` and
`WorkloadMetric`. Checks, manifests and prose stop naming an app.

The names are flat, not nested, because the run-time expander matches a single
dotted identifier (`{{\s*\.(\w+)\s*}}`) deliberately — widening it to accept
dots would start rewriting the Helm, Prometheus and Grafana templating that
legitimately shares those files. A nested `{{.Workload.Name}}` would pass
through unresolved and reach kubectl verbatim.

Incidents keep their `target:` block, which becomes templatable: a fault whose
target reads `{{.WorkloadNamespace}}` follows the binding, while one that pins a
literal keeps breaking exactly the workload it names.

**2a. One package owns the variable set.** The set used to be written out three
times — the catalog validator, the scenario engine, the incident engine — and
the copies had already drifted: `{{.IngressClass}}` resolved at run time but was
rejected by the validator, and the incident engine carried neither it nor
`{{.MonitoringNamespace}}`, so a fault naming one rendered `<no value>`.
`internal/tmpl` now owns both the typed context and the two expansion modes
(strict for validation, lenient for run time), and a reflection test fails if a
field is added without being wired.

**3. The app contract is declared in `app.env`.** That file is already the
contract between an app and the build/deploy engine, it is both shell-sourceable
and viper-readable, and keeping one file per app avoids a second place to look.

| Key | Default | Meaning |
|---|---|---|
| `APP_PORT` | `8080` | Port serving HTTP |
| `APP_HEALTH_PATH` | `/health` | Liveness probe path |
| `APP_READY_PATH` | `/ready` | Readiness probe path |
| `APP_METRICS_PATH` | `/metrics` | Prometheus exposition path |
| `APP_REQUEST_METRIC` | semconv name | The request-duration histogram's name |
| `APP_CAPABILITIES` | *(none)* | Optional behaviours the app claims |

Only capabilities have no default: the base interface is assumed, but a claim is
explicit or absent. The request metric is **named rather than assumed** — each
language's instrumentation library picks its own — while defaulting to the
OpenTelemetry semantic-convention name, so "make your app compatible" teaches a
real standard and an already-instrumented app arrives close to conformant.

**4. Scenarios declare capabilities in `prerequisites.capabilities`.** They sit
with `platform:` and `apps:` because they are a prerequisite of the same kind.
Preflight refuses to activate a scenario the bound app cannot satisfy, before
anything installs, naming every gap at once and how to fix it.

The vocabulary is **closed** — `prometheus-metrics`, `otlp-tracing`,
`readiness-toggle` — because a scenario's requirement and an app's claim are
matched by string, so an open vocabulary would let a typo on either side mean
"never satisfied" with no error anywhere. It is also deliberately short: each
one earns its place by being something a scenario in this repository actually
needs. A survey of all thirteen scenarios found `stateful` was not one —
`backup-restore-drill` and `gitops-cicd` deploy their own PVCs and merely borrow
an app's image — so it was not invented.

**5. Challenges and learning paths are pinned to the default workload.** They
compose scenarios and incidents by reference, so the binding would otherwise flow
straight into them. They are scored and timed: a par time is calibrated against
one workload, and a leaderboard comparing runs on different applications measures
the language rather than the engineer.

`--app` on those commands is an error, not a silent no-op — silently ignoring it
would be worse than either honouring or refusing it. An ambient `APP_NAME` is
pinned quietly instead, because refusing every challenge because `.env` sets a
default would be hostile.

Nothing is lost: a user who wants to see their own application under the same
fault runs that scenario or incident directly with `--app`. The challenge is the
scored wrapper; the incident is the raw experience.

**4a. `labctl app verify` turns a claim into evidence**, running the checks each
capability implies against the deployed workload. It reuses `checks.Check` and
the existing runner rather than bespoke logic, so a claim is verified the same
way a scenario would use it.

Two capabilities are reported as *declared, not machine-checked*:
`otlp-tracing` and `readiness-toggle`. Both are conditional promises — "if you
set this env var I will export", "if you call this endpoint I will go unready" —
so a baseline deployment looks identical whether or not the app honours them.
Grading them would fail a conforming app; asserting they passed would be a lie.
This was found by building the check and watching it fail `go-api`, which does
honour OTLP but only once a tracing scenario wires the endpoint.

**5. Bringing an app reuses the ADR-0008 seam.** Directory discovery plus an
env var, no new distribution mechanism. Two modes:
   - **Pre-built image** — a contract manifest naming image, port, probe paths
     and metric names. Nothing is built.
   - **Source in `apps/`** — the existing `BUILD_STRATEGY` path, unchanged.

**6. Comparison is a first-class operation.** Running one scenario against two
bindings and diffing the result is a product feature, which makes fairness a
requirement rather than a detail: identical traffic profile, an explicit warmup
window excluded from measurement, fixed duration, results persisted per
(scenario, workload) pair.

**7. Thresholds become workload-relative.** Absolute values calibrated to Go —
`latency-within-slo < 1.5`, `readyReplicas >= 3` at 25 RPS/replica — grade the
language, not the engineer, once a JVM app is bound. Each check with an absolute
threshold either moves to a per-workload baseline captured at bind time or
becomes a parameter.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep apps coupled; normalise `oom-kill` to `go-api` | Removes the inconsistency for ~1 day, but forecloses BYO and comparison entirely |
| Bind any app with no declared contract | Every failure becomes ambiguous — "is the lab broken or my app?" — which is exactly the property the simulator exists to avoid |
| Per-app metric-name mapping layer | Avoids touching the apps, but adds indirection to every PromQL check and hides the standard the scenario should be teaching |
| Keep `http_requests_total` as the SnowOps standard | Zero migration now, but every non-Go app needs bespoke instrumentation to conform |
| Multiple roles up front (`queue-consumer`, `stateful-service`) | Speculative; one role covers 12 of 13 scenarios and roles are cheap to add later |

## Consequences

- **Easier:** a user runs any scenario against their own app; comparing stacks
  under an identical fault becomes a supported operation rather than a manual
  re-run.
- **Harder:** content authors must write `{{.WorkloadName}}` instead of a
  literal, and must think about whether a threshold is workload-relative.
- **Migration (done):** the semconv rename touched 13 content and platform files
  plus both apps. It changed the shape of the metrics, not just their names:

  | Before | After |
  |---|---|
  | `http_requests_total` (counter) | *removed* — semconv defines none |
  | `http_request_duration_seconds` (histogram) | `http_server_request_duration_seconds` |
  | labels `method`, `path`, `code` | `http_request_method`, `http_route`, `http_response_status_code` |

  The counter could only be dropped because the status code moved **onto the
  histogram**, which previously carried only `method` and `path`. Without that,
  deriving an error rate from `_count` would have been impossible and every 5xx
  panel would have silently emptied.

  Attribute names were migrated too, not only metric names. That is what makes
  cross-stack comparison work: a Java service instrumented to semconv emits
  `http_response_status_code`, so a dashboard querying `code` would match the Go
  app and quietly show nothing for the Java one.

  Bucket boundaries moved to the semconv-recommended set, which extends to 10s.
  The old set stopped at 1s, so a p99 SLO of 1.5s could never be measured
  honestly — and a JVM app under warmup lives entirely above that ceiling.

  `app` is kept as a label though semconv does not define it: every scenario,
  dashboard and `labctl app verify` selects on it, and it matches the pod label
  Prometheus relabels from.

  Not in scope: `internal/metrics` emits `labctl_`-prefixed metrics for the
  control plane itself, which is a different service and correctly namespaced.
- **Sequencing:** the semconv rename lands *before* content migration, so each
  content file is edited once for both templating and metric names.
- **CI:** e2e stays pinned to the default binding. BYO is covered by a
  conformance suite plus one representative scenario — never a scenario x app
  matrix.
- **Trust:** a brought app runs in the user's cluster under their credentials,
  the same caveat ADR-0008 records for external content.
