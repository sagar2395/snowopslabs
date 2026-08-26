# CLI Reference

`labctl` is the command-line interface for managing the SnowOps Labs homelab.

## Installation

```bash
make cli-build        # builds src/bin/labctl
make cli-install      # builds + copies to PATH
```

## Global Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--project-dir` | string | auto-detected | Project root directory |
| `-v, --verbose` | bool | false | Verbose output |

## Commands

### Lifecycle

#### `labctl init`

Initialize the lab: setup tools, create cluster, install platform components.

```bash
labctl init
```

Equivalent to running `make setup-tools && make runtime-up && make platform-up`.

#### `labctl teardown`

Tear down the lab: destroy apps, remove platform, delete cluster.

```bash
labctl teardown
```

#### `labctl reset`

Full reset: teardown followed by init.

```bash
labctl reset
```

#### `labctl status`

Show overall lab status including cluster info, platform health, and deployed apps.

```bash
labctl status
```

---

### Runtime

Manage the underlying Kubernetes cluster.

#### `labctl runtime up`

Create the cluster using the configured runtime profile (k3d or kind).

```bash
labctl runtime up
```

#### `labctl runtime down`

Destroy the cluster.

```bash
labctl runtime down
```

#### `labctl runtime status`

Show cluster connectivity and node info.

```bash
labctl runtime status
```

---

### Applications

Manage application build and deployment lifecycle.

#### `labctl app list`

List all discovered applications with their build/deploy strategies.

```bash
labctl app list
```

#### `labctl app build <name>`

Build an application container image using its configured build strategy.

```bash
labctl app build go-api
labctl app build echo-server
```

#### `labctl app deploy <name>`

Deploy an application to the cluster using its configured deploy strategy.

```bash
labctl app deploy go-api
```

#### `labctl app destroy <name>`

Remove an application from the cluster.

```bash
labctl app destroy go-api
```

---

### Platform

Manage platform infrastructure components (ingress, monitoring, etc.).

#### `labctl platform up [category|category/provider]`

Install all platform components based on the configured providers, or a single
target when one is named. A target is either a **category** (its provider is
chosen via env var or is the only one) or an explicit **`category/provider`**
spec for additive categories like `data`.

```bash
labctl platform up                         # full stack (ingress + monitoring)
MESH_PROVIDER=istio labctl platform up mesh # a category — provider via env var
labctl platform up data/kafka              # a specific provider (additive)
labctl platform up data/postgres
```

Installs components selected by `INGRESS_PROVIDER`, `METRICS_PROVIDER`,
`MESH_PROVIDER`, etc. in `.env`. When a category has more than one provider and
none is selected, the command lists the choices and the env var to set.

#### `labctl platform down [category|category/provider]`

Uninstall all platform components, or a single target when one is named.

```bash
labctl platform down                       # full stack
MESH_PROVIDER=istio labctl platform down mesh
labctl platform down data/kafka
```

#### `labctl platform status [category|category/provider]`

Show the status of all discovered platform components, or just one target.

```bash
labctl platform status
labctl platform status mesh
labctl platform status data/postgres
```

---

### Scenarios

Manage declarative lab scenarios (observability, security, chaos, etc.).

#### `labctl scenario new <name>`

Scaffold `scenarios/<name>/` with a valid v2 `scenario.yaml` and a passing
readiness check — green under `scenario verify` immediately. `--force` overwrites.
See `docs/authoring/first-scenario.md`.

```bash
labctl scenario new my-first-scenario
```

#### `labctl scenario list`

List all available scenarios with their display names, categories, and activation status.

```bash
labctl scenario list
```

#### `labctl scenario info <name>`

Show detailed information about a scenario: description, prerequisites, components, and exploration hints.

```bash
labctl scenario info observability-sre
```

#### `labctl scenario up <name>`

Activate a scenario. Installs all declared components (Helm charts, manifests, dashboards).

```bash
labctl scenario up observability-sre
```

#### `labctl scenario down <name>`

Deactivate a scenario. Removes installed components.

```bash
labctl scenario down observability-sre
```

#### `labctl scenario status`

Show which scenarios are currently active.

```bash
labctl scenario status
```

#### `labctl scenario verify <name>`

Run the scenario's machine-verifiable `checks` (scenario format v2) and
report pass/fail per check. Exits non-zero if any check fails, so it is
safe in CI and scripts. See `docs/scenarios.md` for the checks schema.

```bash
labctl scenario verify observability-sre

# Re-run until everything passes (useful right after `scenario up`)
labctl scenario verify observability-sre --watch --interval 10s --timeout 5m
```

Flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `--watch` | off | re-run checks until all pass or `--timeout` elapses |
| `--interval` | `10s` | delay between re-runs in watch mode |
| `--timeout` | `5m` | overall watch deadline |
| `--check-timeout` | `30s` | per-check timeout |

`promql` checks query Prometheus at `http://prometheus.<DOMAIN_SUFFIX>` by
default; override with the `PROMETHEUS_URL` environment variable.

The REST equivalent is `POST /api/scenarios/{name}/verify` (synchronous,
bounded to ~12s — use the CLI's `--watch` for long convergence).

---

### Content

#### `labctl validate`

Load and validate every declarative content item — scenarios, incidents,
learning paths, and challenges — against the content model (W2). Checks
required fields, cross-references (a path or challenge must point at content
that exists), and templates (an unknown variable is an error). Reports each
problem as `file:line: [kind/name] message` and **exits non-zero** if any are
found, so CI can gate on it.

```bash
labctl validate            # human-readable report
labctl validate --json     # machine-readable report for tooling
```

Extra content roots named in `SNOWOPS_CONTENT_PATH` (OS-path-list separated)
are discovered and validated alongside the in-repo content, and a later root
overrides an earlier one on a name collision — so you can add or shadow content
without forking the repo. See `docs/runbooks/R03-content-authoring-and-validation.md`
and ADR-0009.

Flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `--json` | off | emit the report (validity, counts, roots, problems) as JSON |

---

### `labctl traffic` — Synthetic load generation

Run a k6 load generator in-cluster so scenarios, incidents, and autoscaling
play out under realistic traffic. The generator lives in its own `traffic`
namespace; scripts are in `services/traffic/`.

#### `labctl traffic start`

Start (or restart — running it again replaces the active run) the generator.

```bash
labctl traffic start                                   # steady 10 rps for 10m at go-api
labctl traffic start --profile spike --rps 20          # 20 rps baseline, 200 rps spike
labctl traffic start --profile soak --duration 4h
labctl traffic start --target http://echo-server.k3d.local/ --rps 50
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--profile` | `steady` | `steady` (constant), `spike` (10x burst, fixed ~6m shape), `soak` (long sustained) |
| `--rps` | `10` | requests/sec (baseline for spike) |
| `--duration` | profile default | run length (`30s`, `10m`, `1h30m`); steady 10m, soak 2h, spike fixed |
| `--target` | go-api `/health` (in-cluster) | URL to load |

Set `K6_PROMETHEUS_RW_SERVER_URL` to also push k6's own metrics into
Prometheus via remote write (requires the receiver enabled); by default,
watch the load through the target app's request metrics in Grafana.

#### `labctl traffic stop`

Stop the generator and remove everything it created (job, configmap, namespace).

#### `labctl traffic status`

Show whether a run is active, its profile, pods, and recent k6 output.

#### `labctl traffic profiles`

List the available profiles (discovered from `services/traffic/profiles/`).

---

### `labctl incident` — Fault injection & game days

Inject realistic, reversible production faults from `incidents/` and
practice diagnosing them; the fault's detection check confirms when you've
actually fixed it. See `incidents/README.md` for the fault contract.

```bash
labctl incident list                          # browse the fault library
labctl incident inject oom-kill               # break the lab on purpose
labctl incident inject --random --silent      # game-day mode: surprise fault, name hidden
labctl incident inject --random --seed 42     # reproducible pick (same fault for the whole team)
labctl incident inject --random --category network
labctl incident status                        # runs the detection check; clears state when it passes
labctl incident hint                          # next progressive hint (recorded — costs score later)
labctl incident solution [--yes]              # full walkthrough (spoiler; asks for confirmation)
labctl incident history                       # past runs: time-to-check, MTTR, hints used, resolved-by
labctl incident resolve                       # escape hatch: undo the active fault
labctl incident resolve <name>                # works even if active state was lost
```

Rules: one active incident at a time (`--force` to override); injection is
gated on the fault's prerequisite apps being present; `resolve.sh` always
restores the lab regardless of partial manual fixes.

Timing: the first `incident status` call timestamps "time-to-check"
(detection proxy); resolution (detection check passing, or the escape
hatch) closes the run. Each run is appended to
`.labctl/history/incidents.jsonl` with MTTR, hints used, and whether it was
resolved manually or via `resolve` (the latter counts as a non-completion
for future challenge scoring).

On-call drills: faults with an `expectAlert` (`oom-kill`,
`crashloop-bad-config`, `bad-deploy-rollout`) arm a PrometheusRule on
injection; Alertmanager routes the firing alert to the in-cluster pager
(`labctl service up pager`) or to `ALERT_WEBHOOK_URL` if set when
installing `monitoring/metrics`. `incident status` shows whether the page
fired by querying Alertmanager (`ALERTMANAGER_URL`, default
`http://alertmanager.<DOMAIN_SUFFIX>` via the ingress created by the
metrics provider). See runbook 08 §9 for the full drill.

REST: `GET /api/incidents`, `POST /api/incidents/{name}/inject[?silent&force]`,
`POST /api/incidents/inject-random[?seed&category]`, `GET /api/incidents/status`,
`POST /api/incidents/hint`, `GET /api/incidents/history`,
`POST /api/incidents/resolve[?name=]`. Silent mode hides the fault's identity
in API responses until it is resolved.

---

### `labctl lab` — Snapshot, restore, reset

Lab-level state operations for fast iteration. A snapshot records **intent**
(which platform components, apps, and scenarios are active) as a small YAML
file in `.labctl/snapshots/` — not cluster bytes. Restore replays the normal
idempotent install paths; reset tears everything down to post-init.

```bash
labctl lab snapshot before-gameday    # record current state
labctl lab snapshots                  # list saved snapshots
labctl lab reset                      # interactive teardown (keeps cluster + ingress)
labctl lab reset --yes                # non-interactive
labctl lab restore before-gameday     # converge back: platform → apps → scenarios
labctl lab delete before-gameday
```

Details:

- **Snapshot sources** — platform components from labctl's install markers
  (`.labctl/platform/`, written by `platform up`/`down`; installs done
  outside labctl are not tracked), scenarios from the scenario engine's
  state, apps by live kubectl probe.
- **Restore order** — ingress first, then monitoring, then remaining
  platform components, then apps, then scenarios. Already-active pieces are
  skipped (idempotent), so restoring over a half-converged lab is safe.
- **Reset** — stops traffic, deactivates all scenarios, destroys deployed
  apps, uninstalls platform components **except the ingress category**, and
  keeps going past individual failures, reporting what stuck at the end.
  The cluster itself stays up.

REST equivalents: `GET/POST/DELETE /api/lab/snapshots[/{name}]`,
`POST /api/lab/snapshots/{name}/restore` (async, returns a job id),
`POST /api/lab/reset?confirm=true` (async; refuses without `confirm`).

---

### Services

Manage shared services (Redis, etc.) that apps depend on.

#### `labctl service list`

List all available shared services.

```bash
labctl service list
```

#### `labctl service up <name>`

Install a shared service.

```bash
labctl service up redis
```

#### `labctl service down <name>`

Uninstall a shared service.

```bash
labctl service down redis
```

#### `labctl service status [name]`

Show service status. If no name is given, shows all services.

```bash
labctl service status
labctl service status redis
```

---

### Checks

Run validation checks against the environment.

#### `labctl check tools`

Verify that all required CLI tools (kubectl, helm, docker, etc.) are installed and accessible.

```bash
labctl check tools
```

#### `labctl check cluster`

Check cluster connectivity via `kubectl cluster-info`.

```bash
labctl check cluster
```

#### `labctl check ingress`

Check that the ingress controller is running and responding.

```bash
labctl check ingress
```

---

### `labctl learn` — Guided learning paths

Work through step-by-step learning paths that combine cluster setup, app
deployment, scenarios, and incidents into structured modules with
machine-verifiable completion checks.

Progress is persisted in `.labctl/learn/` and survives CLI restarts.

#### `labctl learn list`

List all available learning paths with your current progress.

```bash
labctl learn list
```

#### `labctl learn start <path>`

Start (or restart) a learning path.

```bash
labctl learn start kubernetes-foundations
```

#### `labctl learn next [path]`

Show the next incomplete module's intro and objective, then run its
completion check. Run this again after completing the task to verify
and advance.

```bash
labctl learn next kubernetes-foundations           # verify + advance
labctl learn next kubernetes-foundations --show-only  # show intro without checking
```

If there is only one path available, the path argument can be omitted.

#### `labctl learn progress [path]`

Show detailed progress for a path (or all paths if no argument given).

```bash
labctl learn progress
labctl learn progress kubernetes-foundations
```

---

### `labctl challenge` — Timed skills assessments

Inject real faults (or activate scenarios), race against the par time,
and get an auto-graded score on submit. Each hint you take costs score
points.

```bash
labctl challenge list
labctl challenge info restore-broken-deploy
labctl challenge start restore-broken-deploy     # injects fault + starts timer
labctl challenge status                          # elapsed time + hints used
labctl challenge hint                            # next hint (-5% score per hint)
labctl challenge submit                          # run checks, compute score, record result
labctl challenge abort                           # undo setup, score as aborted
labctl challenge history                         # past runs with MTTR, score, hints
```

**Score formula:** `100 − (hints × penalty) − time_over_par_penalty`, scaled
by the fraction of checks that pass. See `challenges/README.md` for details.

REST: `GET /api/challenges`, `GET /api/challenges/{name}`,
`GET /api/challenges/status`, `GET /api/challenges/history`,
`POST /api/challenges/complete`.

---

### `labctl env` — Multi-environment release pipeline

Manage the three simulated environments (`dev`, `staging`, `prod`) deployed by
the `env-promotion` scenario. Each environment runs in its own namespace with a
declared image tag tracked in a ConfigMap.

Activate the environments first: `labctl scenario up env-promotion`

#### `labctl env list`

Print a table of environments, their declared image tags, and readiness.

```bash
labctl env list
```

Example output:

```
ENV        APP          TAG          STATUS     PROMOTED_AT
dev        go-api       v1.2.0       running    never
staging    go-api       v1.1.0       running    never
prod       go-api       v1.0.0       running    never
```

#### `labctl env promote <from-env> <to-env>`

Promote an app's declared image tag from one environment to the next.

```bash
labctl env promote dev staging      # advance staging to dev's tag
labctl env promote staging prod     # release to prod
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--app` | string | `go-api` | App to promote |

Promotion is idempotent: if the destination already has the source's tag,
the command reports "nothing to promote" and exits cleanly.

**Runbook:** `docs/runbooks/11-multi-env-day2.md`

---

### Web UI

#### `labctl ui`

Launch the web UI dashboard. Opens a browser automatically.

```bash
labctl ui                # default port 3939
labctl ui --port 8080    # custom port
```

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--port` | string | 3939 | Port to serve the UI on |

The dashboard shows:
- Cluster status and connection info
- Platform component health
- Applications with deploy/destroy actions
- Scenarios with activate/deactivate controls
- Real-time updates via WebSocket

### `labctl users` — Team mode (auth & RBAC)

Manage accounts for the API/UI when authentication is enabled. Authentication is
**off by default** — the server only enforces it when started with
`LABCTL_AUTH=true`. These commands edit `.labctl/users.yaml` (PBKDF2-HMAC-SHA256
password hashes, mode 0600) regardless of whether auth is currently on.

Two roles exist: `operator` (full control) and `participant` (run
challenges/incidents/learn + read status; cannot mutate
platform/runtime/lab/apps/services).

```bash
labctl users add alice --role operator --password 's3cret'
labctl users add bob   --role participant            # prompts for password
LABCTL_PASSWORD='pw' labctl users add carol --role participant   # scripted
labctl users list                                    # NAME / ROLE (never hashes)
labctl users remove bob
```

| Command | Flags | Description |
|---------|-------|-------------|
| `users add <name>` | `--role` (operator\|participant, default participant), `--password` | Add or update a user. Password precedence: `--password` → `LABCTL_PASSWORD` → stdin prompt. |
| `users list` | — | List users and roles. |
| `users remove <name>` | — | Delete a user. |

Enable auth and start the server:

```bash
LABCTL_AUTH=true labctl ui --port 3939
```

When enabled, the UI shows a login screen; the API requires a session cookie or
`Authorization: Bearer <token>` (from `POST /api/auth/login`). Participants get
**403** on operator-only mutations. Scored runs are attributed to the
authenticated user in the results store. Full walkthrough:
`docs/runbooks/12-team-mode.md`.

> OIDC/SSO is out of scope for v1. Serve behind TLS for non-localhost use — the
> session cookie is `HttpOnly` + `SameSite=Strict` but not `Secure`.

## Comparison: CLI vs Make

Both interfaces work. Use whichever you prefer:

| Operation | CLI | Make |
|-----------|-----|------|
| Full setup | `labctl init` | `make init` |
| Build app | `labctl app build go-api` | `make build APP_NAME=go-api` |
| Deploy app | `labctl app deploy go-api` | `make deploy APP_NAME=go-api` |
| Platform status | `labctl platform status` | `make platform-status` |
| Activate scenario | `labctl scenario up observability-sre` | N/A (CLI only) |
| Web dashboard | `labctl ui` | N/A (CLI only) |
| Deploy all apps | N/A | `make deploy-all` |

The CLI adds scenario management, the web UI, and a unified status view. Make targets are more granular.
