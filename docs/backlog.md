# Engineering backlog

Deferred work that is understood but not yet done. Each item says what is
wrong, how it was found, the proposed approach, and where to start, so a later
session can pick one up without re-deriving it.

To pick one up: read the item, read the documents it links, and follow the
[definition of done](AGENT-CONTEXT.md#definition-of-done). When it lands, delete
the item — git history keeps the record.

Effort is in engineering days and is an estimate, not a commitment.

| # | Item | Area | Effort |
|---|---|---|---|
| [B1](#b1--activate-one-scenario-for-two-apps-at-once) | Activate one scenario for two apps at once | engine, content, UI | 18–22 |
| [B2](#b2--k6-metrics-carry-no-app-label) | k6 metrics carry no app label | traffic, dashboards | 1–1.5 |
| [B3](#b3--the-shared-request-dashboard-only-sees-the-default-metric-name) | The shared request dashboard only sees the default metric name | dashboards | 1–2 |
| [B4](#b4--two-scenarios-can-mutate-the-same-app-at-once) | Two scenarios can mutate the same app at once | engine | 1–2 |
| [B5](#b5--activating-from-the-ui-does-not-report-missing-hostnames) | Activating from the UI does not report missing hostnames | API, UI | 0.5–1 |
| [B6](#b6--incident-scripts-still-fall-back-to-go-api) | Incident scripts still fall back to go-api | content | 0.5–1 |
| [B7](#b7--a-docker-vm-restart-silently-kills-k3d-nodes) | A Docker VM restart silently kills k3d nodes | runtime, CLI | 1 |
| [B8](#b8--scenario-down-reports-success-when-its-deletes-fail) | `scenario down` reports success when its deletes fail | engine, content | 1–1.5 |
| [B9](#b9--latency-below-5ms-is-invisible-and-it-hides-tail-amplification) | Latency below 5ms is invisible, and it hides tail amplification | apps, grading | 1–2 |
| [B10](#b10--chaos-network-faults-never-reach-the-traffic-generator) | Chaos network faults never reach the traffic generator | content, traffic | 1–2 |
| [B11](#b11--secrets-management-only-works-for-the-app-the-platform-was-installed-for) | secrets-management only works for the app the platform was installed for | platform, content | 1 |
| [B12](#b12--every-k3d-node-promises-the-whole-machines-memory) | Every k3d node promises the whole machine's memory | runtime | 1 |
| [B13](#b13--scenarioengineget-writes-to-a-shared-cached-scenario) | `scenario.Engine.Get` writes to a shared, cached scenario | engine | 0.5 |
| [B14](#b14--ctrl-c-orphans-running-scripts) | Ctrl-C orphans running scripts | CLI, run engine | 1 |
| [B15](#b15--two-submits-can-take-the-same-lock) | Two submits can take the same lock | run engine, store | 0.5–1 |
| [B16](#b16--web-ui-actions-bypass-the-run-engine) | Web UI actions bypass the run engine | API | 4–6 |
| [B17](#b17--api-request-bodies-are-unbounded) | API request bodies are unbounded | API | 0.25 |
| [B18](#b18--internalcli-runs-on-package-level-state) | `internal/cli` runs on package-level state | CLI | 3–4 |
| [B19](#b19--smaller-go-clean-ups) | Smaller Go clean-ups | Go | 1–2 |

---

## B1 — Activate one scenario for two apps at once

**Problem.** A scenario can be active for exactly one app. Its name is the only
identity everywhere: the `.active` marker, the run lock (`LockKey(name)`), the
component inventory's `owner`, the API routes (`/api/v2/scenarios/{name}/…`),
the dashboard ConfigMap (`scenario-<name>-<file>`) and verify results. A second
activation overwrites the first, and `down` for one app uninstalls shared pieces
(Loki, Tempo, Kyverno, ArgoCD, dashboards) the other still uses.

**Already parallel-ready.** Scripts take `--app`/`--namespace` flags
([ADR-0015](adr/0015-learner-facing-workload-binding.md)), scenario dashboards
select the app with `$app`, and `labctl hosts add` reads hostnames from Ingress.
None of those need to change.

**Proposed approach.**

1. Make an activation `(scenario, app)`: marker `<name>@<app>.active`, lock key
   per activation plus a scenario-level key for shared components, inventory
   owner including the app, results keyed by both.
2. Reference-count shared components (helm charts, cluster-scoped manifests,
   ServiceMonitors, dashboards) so `down` removes them only with the last
   activation. This is the real engine work.
3. API: `/api/v2/scenarios/{name}/activations/{app}` or an `app` query parameter;
   CLI `verify`/`down`/`status` require `--app` when more than one is active.
4. UI: a scenario card lists its activations; verify and teardown per activation.
5. One traffic run per app (see [B2](#b2--k6-metrics-carry-no-app-label)).
6. Content, by readiness:
   - ready once the engine supports it — autoscaling-under-load,
     backup-restore-drill, secrets-management, mesh-traffic-management,
     cost-right-sizing, chaos-engineering;
   - needs redesign — env-promotion (fixed `env-dev/staging/prod` namespaces and
     one `env-metadata` ConfigMap per env), observability-sre (fixed
     PrometheusRule name, alert checks keyed on the scenario), security-compliance
     (cluster-wide ClusterPolicies change what the other activation is graded on);
   - single-instance by nature, refuse a second activation in preflight —
     node-drain-drill and cluster-upgrade-drill (they drain or replace nodes under
     every app), event-driven-arch and gitops-cicd (they do not use the app).

**Caveat.** Two apps under load on one k3d node contend for CPU, which is why
`labctl compare` runs apps one at a time. Parallel activation is for learning,
not for comparing performance grades.

**Start at.** `src/internal/scenario/engine.go` (`markActive`, `activationRecord`),
`src/internal/service/scenario/scenario.go` (`LockKey`),
`src/internal/store/migrations/0002_components.sql`.

---

## B2 — k6 metrics carry no app label

**Problem.** There is one cluster-wide k6 Job (`traffic-k6`) and its remote-written
metrics (`k6_http_reqs_total`, `k6_http_req_duration_p95`, …) have no `app`
label. On Application Request Metrics the "Offered vs served" panel therefore
compares all k6 traffic with whichever apps the App selector picks, and the k6
panels cannot be filtered at all (they carry a description saying so). Two
activations can never both be under load.

**Proposed approach.** Pass the target app into the run (`labctl traffic start
--app` already resolves it) and tag it: `k6 run --tag app=<app>`, which the
Prometheus remote-write output turns into a label. Name the Job per app
(`traffic-k6-<app>`) so runs can coexist, and make `traffic stop`/`status` accept
`--app`. Then filter the k6 panels by `app=~"$app"` and remove their
descriptions. Verify the label on a live lab before changing dashboards.

**Start at.** `src/services/traffic/start.sh`, `src/internal/traffic/traffic.go`,
`src/internal/cli/traffic.go`,
`platform/monitoring/grafana/provisioning/dashboards/app-requests.json`.

---

## B3 — The shared request dashboard only sees the default metric name

**Problem.** An app declares its request histogram in `app.env`
(`APP_REQUEST_METRIC`, free-form). Scenario dashboards render the bound app's
name, but the platform dashboards ship as a raw ConfigMap and cannot be
templated, so Application Request Metrics queries
`http_server_request_duration_seconds` literally. A bring-your-own app that
declares a different name never appears there, and never appears in a scenario
dashboard's App selector either.

**Proposed approach.** Either make the contract require the semconv name (and
reject anything else in `workload.ParseContract`), or add a recording rule per
deployed app that normalises its histogram to one series name with the `app`
label, installed by `labctl app deploy`. Prefer the first unless a real app needs
the second.

**Start at.** `src/internal/workload/contract.go`, `apps/README.md`.

---

## B4 — Two scenarios can mutate the same app at once

**Problem.** Scenarios share the app they are bound to. cost-right-sizing inflates
its resource requests, autoscaling-under-load scales it and measures latency,
node-drain-drill and cluster-upgrade-drill scale it to several replicas, and
observability-sre rewrites its environment. Nothing warns when two such scenarios
are active for the same app, so one silently changes what the other grades.

**Proposed approach.** Declare in `scenario.yaml` whether a scenario mutates its
workload (e.g. `workload: exclusive`) and have preflight refuse, or warn about, a
second exclusive activation on the same app. Measure which scenarios actually
conflict before choosing refuse over warn.

**Start at.** `src/internal/scenario/engine.go` (preflight),
`docs/reference/scenario-schema.md`.

---

## B5 — Activating from the UI does not report missing hostnames

**Problem.** `labctl scenario up` and `labctl app deploy` print Ingress hostnames
that `/etc/hosts` does not list yet (`warnMissingHosts`). Activating from the UI
prints nothing, so a learner there first learns of a new hostname when the
browser fails to resolve it — env-promotion's `<app>-dev|staging|prod` hosts are
the common case.

**Proposed approach.** Expose the missing hosts on the scenario detail or status
response (reading `/etc/hosts` needs no privileges), and show a notice with the
`labctl hosts add` command after activation.

**Start at.** `src/internal/cli/hosts.go` (`unlistedHosts`),
`src/internal/httpapi/handlers.go`, `src/ui/src/views/Scenarios.tsx`.

---

## B6 — Incident scripts still fall back to go-api

**Problem.** Scenario scripts take the binding through
`scenarios/_lib/workload.sh` and fail loudly without one. Incident scripts
(`incidents/*/inject.sh`, `resolve.sh`, `checks/resolved.sh`,
`incidents/_lib/render.sh`) still read `${TARGET_WORKLOAD:-go-api}`. The engine
always exports the target, so this is not a live bug, but a missing binding would
silently act on go-api instead of failing.

**Proposed approach.** Give incidents the same rule: a shared helper that requires
`TARGET_NAMESPACE`/`TARGET_WORKLOAD` (flags for hand runs), and remove the
fallbacks. Review each fault with the incident review harness afterwards.

**Start at.** `incidents/_lib/`, [incidents/README.md](../incidents/README.md).

---

## B7 — A Docker VM restart silently kills k3d nodes

**Problem.** k3d nodes share the Docker VM's kernel, whose
`fs.inotify.max_user_instances` defaults to 128 on colima. `runtimes/k3d/up.sh`
raises it to 512 (`raise_inotify_limits`), but only when `up` runs. A VM restart
resets it, and a node's k3s agent later dies with `failed to create inotify fd:
too many open files`. The container stays `Up`, so nothing looks wrong from
Docker; the node goes NotReady, its pods sit in `Terminating` forever and their
replacements go `Pending`. Found on a live lab where one node had been NotReady
for 21 hours, wedging `env-prod` in `Terminating` and leaving the chaos,
external-secrets, gitops and Kafka workloads half-scheduled.

**Recovery today.** Raise the limit on every node, then restart the dead node's
container:

```bash
for n in $(docker ps --filter label=k3d.cluster=snowops --format '{{.Names}}'); do
  docker exec "$n" sysctl -w fs.inotify.max_user_instances=512
done
docker restart k3d-snowops-agent-1-0
```

**Proposed approach.** Re-apply the limit wherever labctl touches a running
cluster, not only at creation — `labctl status`, `labctl doctor`, and the start of
every run-engine operation are candidates. Have `labctl status` report a node
whose k3s process is gone while its container is up, with the recovery above.

**Start at.** `runtimes/k3d/up.sh` (`raise_inotify_limits`),
`src/internal/cli/status.go`, `src/internal/cli/doctor.go`.

---

## B8 — `scenario down` reports success when its deletes fail

**Problem.** `Engine.Down` prints an uninstall error as a warning, carries on,
removes the `.active` marker and returns success. On a lab whose API server was
timing out, `scenario down event-driven-arch` and `scenario down gitops-cicd`
both reported `succeeded` while every `kubectl delete` failed with `TLS
handshake timeout`. The orders producers and consumers, the `orders` topic, the
ArgoCD Applications, the `gitops-demo` and `gitops-broken` namespaces, the
ServiceMonitors and PodMonitors, and the dashboard ConfigMaps were all left
running. Because the marker was gone, `scenario down` then refused with "not
active", so the only way out was deleting by hand. `scripts/remove-apps.sh`
makes it worse by printing `✓ gitops-demo and gitops-broken removed` after its
own deletes failed (every command ends in `|| true`).

**Proposed approach.**

1. Collect uninstall errors in `Down`. When any component fails, keep the
   marker, name the failed components, and return an error telling the user to
   re-run `labctl scenario down <name>`; every uninstall is already idempotent
   (`--ignore-not-found`, `helm uninstall` of a missing release).
2. Add `labctl scenario down --force` that clears the marker whatever failed, for
   a component that can never be deleted again (a CRD already removed makes
   `kubectl delete -f` fail with "no matches for kind").
3. Make uninstall scripts report failure: drop `|| true` where a failure means
   something was left behind, and print the success line only when the deletes
   succeeded.
4. Test: an executor fake whose delete fails must leave the scenario active.

**Start at.** `src/internal/scenario/engine.go` (`Down`, `uninstallComponent`),
`scenarios/gitops-cicd/scripts/remove-apps.sh`.

---

## B9 — Latency below 5ms is invisible, and it hides tail amplification

**Problem.** The request histogram's first bucket ends at 5ms (`le` = 0.005, 0.01,
0.025, …, 10). Go and the JVM both answer the lab's routes in about a
millisecond, so every request lands in that bucket and `histogram_quantile` can
only interpolate inside it. Measured under k6 load on go-api, echo-server and
java-api at once: p50 = 2.5ms, p95 = 4.75ms and p99 = 4.95ms for every app, while
k6 measured p95 around 0.7ms. The dashboards now say so in the panel
description, but grading cannot be fixed with a description:
autoscaling-under-load's `latency-did-not-degrade` divides p99 by p50, which is
pinned at 4.95 / 2.5 = 1.98 until the p50 itself passes 5ms. With the default
`MaxTailAmplification` of 10, the check cannot fail while the service is fast,
and only starts measuring once it is already badly saturated.

**Proposed approach.** Add sub-5ms buckets to the app contract (for example
0.0005, 0.001, 0.0025 ahead of the semconv set) in go-api, echo-server, java-api
and the shared chart's documentation, and state the required bucket floor in
`apps/README.md`. Then re-measure tail amplification under the spike profile on
each app and recalibrate the `MaxTailAmplification` default, as the scenario
review harness does. Consider whether `labctl app verify` should reject a
histogram with no bucket below 5ms.

**Start at.** the histogram definitions in `apps/go-api`, `apps/echo-server` and
`apps/java-api`, `scenarios/autoscaling-under-load/scenario.yaml`,
[scenario review](authoring/scenario-review.md).

---

## B10 — Chaos network faults never reach the traffic generator

**Problem.** chaos-engineering's `network-delay` and `network-partition`
experiments act only between the workload and the `traefik` namespace
(`direction: both`, `target.selector.namespaces: [traefik]`). `labctl traffic`
runs k6 inside the cluster against the Service, which never crosses Traefik. So
under the scenario's own load the client-side panels cannot show either fault:
with a 300ms delay injected against echo-server, k6's p99 stayed at 1.4ms and the
server-side p99 did not move either. The dashboard panel was titled "where a
network delay shows up"; it now says the fault is not on k6's path and points to
the Explore tab's curl through the ingress. The `blast-radius-contained` check
and the scenario prose should be re-read with this in mind.

**Proposed approach.** Either route k6 through the ingress (target Traefik's
Service with the app's `Host` header, which `labctl traffic start` would need a
`--via-ingress` option for), or widen the experiments' target to also include
the `traffic` namespace so in-cluster load is affected. Re-review the scenario
with the scenario review harness afterwards.

**Start at.** `scenarios/chaos-engineering/manifests/chaos-experiments.yaml`,
`src/services/traffic/start.sh`, [scenario review](authoring/scenario-review.md).

---

## B11 — secrets-management only works for the app the platform was installed for

**Problem.** `platform/secrets/external-secrets/install.sh` creates the
SecretStore and the `<workload>-secret` ExternalSecret for whichever app is bound
when the platform component is installed (`${WORKLOAD_NAME:-go-api}`). The
scenario then assumes that ExternalSecret exists for the app it is activated
with. On a lab whose platform was installed for go-api, `labctl scenario up
secrets-management --app java-api` fails in `seed-baseline.sh` with
`externalsecrets.external-secrets.io "java-api-secret" not found` — and the
failed activation leaves `secret-consumer` and `env-consumer` running in the
`java-api` namespace, because a failed `up` does not roll back what it already
applied (compare [B8](#b8--scenario-down-reports-success-when-its-deletes-fail)).

**Proposed approach.** Move the per-app wiring out of the platform component:
the platform installs ESO, Vault and a ClusterSecretStore; the scenario applies
the `<workload>-secret` ExternalSecret for its own binding as a manifest
component, so any app works and teardown removes it. Separately, decide whether
a failed `scenario up` should uninstall the components it already applied.

**Start at.** `platform/secrets/external-secrets/install.sh`,
`scenarios/secrets-management/scenario.yaml`,
`scenarios/secrets-management/scripts/seed-baseline.sh`.

---

## B12 — Every k3d node promises the whole machine's memory

**Problem.** k3d nodes are containers on one Docker VM, and each kubelet reports
the VM's full memory as allocatable. On an 8GB colima VM the three nodes
advertised 8.3GB each, about 25GB in total, so the scheduler kept placing pods
(the server node alone reached 4.3GB of requests) until the VM had 34MB free and
a load average near 440. The API server then timed out, a node's k3s agent died,
and `scenario down` silently failed its deletes (see
[B7](#b7--a-docker-vm-restart-silently-kills-k3d-nodes) and
[B8](#b8--scenario-down-reports-success-when-its-deletes-fail)). Seven scenarios
active at once is enough to get there.

**Proposed approach.** Give each node a real share when the cluster is created:
`--kubelet-arg=system-reserved=memory=…` (or `kube-reserved`) sized so that the
nodes' allocatable memory sums to what the VM has, computed from `docker info`
in `runtimes/k3d/up.sh`. Have `labctl doctor` or `labctl status` warn when total
memory requests approach the VM's memory, and document a memory budget per
scenario so learners know how many they can run at once.

**Start at.** `runtimes/k3d/up.sh` (`create_cluster`), `src/internal/cli/doctor.go`.

---

## B13 — `scenario.Engine.Get` writes to a shared, cached scenario

**Problem.** `Get` returns the `*Scenario` held in the engine's cache, but only
after setting `s.Active` on it. Any two goroutines that call `Get` at the same
time, or one that calls `Get` while a run reads the scenario, race on that
field. `go test -race ./internal/service/scenario/` reports it on `main` in
`TestActivate_ConflictsPerScenario`, which fails most runs. The same race is
live in `labctl ui`, because its handlers call `Get` concurrently.

**Proposed approach.** Stop writing to the cached value. Either return a
shallow copy with `Active` filled in, or drop the field and have callers ask
`IsActive(name)`. Take the copy approach first: it keeps every caller the same.

**Start at.** `src/internal/scenario/engine.go` (`Get`, `isActive`).

---

## B14 — Ctrl-C orphans running scripts

**Problem.** `labctl` never installs a signal handler, so `cmd.Context()` is
`context.Background()` and Ctrl-C kills the process with Go's default action.
Scripts started through `internal/toolchain` run in their own process group
(`Setpgid`), so the terminal's SIGINT doesn't reach them. They keep running
with no parent, and their run stays `running` until the next engine start
reconciles it. That breaks the promise in
[ADR-0003](adr/0003-durable-run-engine.md). The same gap has two smaller
effects:

- `labctl ui` exits without calling `Shutdown` on the HTTP server.
- `scenario verify --watch` sleeps with `time.Sleep`, which no context can
  interrupt.

**Proposed approach.** In `cli.Execute`, run the root command under
`signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` via
`rootCmd.ExecuteContext`. Everything already reads `cmd.Context()`, so
cancellation then flows through to `run.Engine` and each process group gets its
graceful SIGTERM. Give `labctl ui` an `http.Server.Shutdown` on the same
context, and replace the sleep with a `select` on `ctx.Done()` and a
`time.After`.

**Start at.** `src/internal/cli/root.go` (`Execute`), `src/internal/cli/ui.go`,
`src/internal/cli/scenario.go` (verify `--watch`).

---

## B15 — Two submits can take the same lock

**Problem.** `run.(*Engine).Submit` checks `ActiveRunForLock` and then calls
`CreateRun` as two separate statements. The index on `runs.lock_key` isn't
unique, so two submits that land between those two calls both succeed. That
can happen with two UI requests, or with the CLI in one terminal and the UI in
another. Two runs then hold one lock, which is exactly what
[ADR-0004](adr/0004-lock-and-reject-concurrency.md) exists to prevent.

**Proposed approach.** Make the check and the insert one atomic statement, so it
also holds across processes: `INSERT … SELECT … WHERE NOT EXISTS (SELECT 1 FROM
runs WHERE lock_key = ? AND status IN ('queued','running'))`. Then report zero
rows affected as the `*LockConflictError`. A partial unique index on
`lock_key WHERE status IN (…) AND lock_key != ''` is the alternative. It needs
a migration.

**Start at.** `src/internal/run/engine.go` (`Submit`),
`src/internal/store/runs.go` (`CreateRun`, `ActiveRunForLock`).

---

## B16 — Web UI actions bypass the run engine

**Problem.** The `/api/v2` handlers for app deploy, destroy and build, platform
install and uninstall, scenario up and down, services, runtimes, lab restore
and reset, and traffic start their work in a detached `go func()` through
`internal/executor`. That executor calls `exec.Command` with no context, takes
no lock, and keeps its job history in memory. None of that work can be
cancelled, none of it is refused on a conflict, and none of it survives a
restart. The CLI's `lab`, `platform` and `incident` commands, meanwhile, go
through `internal/service` and the run engine. The two interfaces therefore
behave differently for the same operation, against both the
[architecture](architecture/ARCHITECTURE.md#1-the-shape-of-the-system) and the
"everything that shells out goes through `internal/run`" invariant.

**Proposed approach.** Move one route family at a time onto the service that
already exists: platform first (its service is complete), then scenario, lab,
app and traffic. Each handler submits a `run.Spec` and returns `202` with the
run ID, and the UI streams it from `/api/v2/runs/{id}`. Delete
`internal/executor` once no handler uses it.

**Start at.** `src/internal/httpapi/handlers.go` (every `go func()`),
`src/internal/httpapi/lab.go`, `src/internal/httpapi/traffic.go`,
`src/internal/service/`.

---

## B17 — API request bodies are unbounded

**Problem.** No handler or middleware limits the request body size.
`json.NewDecoder(r.Body)` reads whatever arrives. With auth on and
`--bind 0.0.0.0`, an authenticated client can make the server buffer an
arbitrarily large body.

**Proposed approach.** Add `http.MaxBytesReader` in the middleware chain that
already wraps every route. 1 MiB is far more than any request body the API
takes. Return `413` through `respondError`.

**Start at.** `src/internal/httpapi/middleware.go`, `src/internal/httpapi/server.go`.

---

## B18 — `internal/cli` runs on package-level state

**Problem.** The CLI's configuration, executor and engines (`cfg`,
`scriptExec`, `reg`, `scenes`, `incEng`, `svcReg`, `rtm`) are package
variables that `PersistentPreRunE` assigns and `bindWorkload` reassigns. Most
commands are also package variables, registered in `init()`, with their flags
bound to more package variables. The newer `learn`, `challenge`, `lab`,
`runs`, `validate` and `doctor` trees are built by constructors instead. As a result:

- Tests share state and can't run in parallel.
- Command wiring depends on file init order.
- A reader can't tell from a function's signature what it depends on.

**Proposed approach.** Introduce an `app` struct holding what
`PersistentPreRunE` builds, and give each command a constructor that takes it
(`func scenarioCmd(a *app) *cobra.Command`). Convert one command group per PR,
starting with the smallest (`runtime`, `service`, `check`).
[Go conventions §6](GO-CONVENTIONS.md#6-cli-commands) already requires the
constructor form for new commands.

**Start at.** `src/internal/cli/root.go`.

---

## B19 — Smaller Go clean-ups

Each is small, safe and independent:

- `scenario.NewEngine` takes `monitoringNamespace ...string` to fake an
  optional parameter. The one production caller passes three arguments and then
  sets the field anyway. Drop the variadic and set the field.
- `runDoctor` and `dockerResourceWarning` accept a `nil` context and replace it
  with `Background`, only because their tests pass `nil`. Pass `t.Context()` in
  the tests and delete the nil checks.
- `labctl ui` launches the browser before checking that the port is free. It
  also probes the port with `net.Listen`, closes it and then listens again,
  which is racy. Build the listener once and hand it to `http.Server.Serve`,
  and open the browser only after that succeeds.
- `fs.Sub(webui.DistFS, "dist")` discards its error in `ui.go`.
- Seven JSON struct fields carry `omitempty` on a struct type, where it has no
  effect. Decide per field whether the API should omit the zero value
  (`omitzero`) or always send it (drop the tag). `omitzero` changes the
  response, so check the UI first.
- `pkg/checks` and `pkg/extension` shell out with `exec.CommandContext`
  directly. That's allowed, because `pkg/` can't import `internal/run`, but
  `pkg/checks.NewRunner` builds an `http.Client` with no `Timeout`. Only the
  per-check context bounds a request. Set a client timeout as a second guard.
- The largest files mix several concerns and are the hardest to review:
  `internal/scenario/engine.go` (1,450 lines), `internal/httpapi/handlers.go`
  (1,070), `internal/incident/incident.go` (750). Split them by concern
  (install, state, templates; one handler file per resource), moving code
  without changing it.

**Start at.** The file named in each bullet.

