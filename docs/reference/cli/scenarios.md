# Scenarios

Scenarios are declarative labs: a YAML file naming the platform components,
apps and dashboards to install, plus machine-verifiable checks that grade your
work. The schema is [scenario schema](../scenario-schema.md); the catalog of
what ships is [scenarios](../../scenarios.md).

## Browsing and running

```bash
labctl scenario list                        # names, categories, active state
labctl scenario list -o json                # the same as a JSON array, for scripts
labctl scenario info observability-sre      # description, prerequisites, components, hints
labctl scenario up observability-sre        # activate: install every declared component
labctl scenario down observability-sre      # deactivate: remove them
labctl scenario status                      # which scenarios are active
```

`scenario list -o json` (`--output json`) prints only a JSON array on stdout,
one object per scenario with the fields `GET /api/v2/scenarios` returns:
`name`, `displayName`, `description`, `category`, `runtimes`, `active` and
`app`. Load warnings go to stderr, so `labctl scenario list -o json | jq`
works. The default, `-o table`, is the table above; any other value is an
error that lists the two.

`scenario info` on an active scenario describes it for the app it was activated
with, so its manifests, snippets and commands name that app, not the default.

`scenario down` tries every component's uninstall, even after one fails. If
any fails, the scenario stays active, the command exits non-zero and names
what is left; every uninstall is safe to repeat, so run it again once the
cluster answers. For a component that can never be removed again, such as one
whose CRD is already gone, `labctl scenario down <name> --force` marks the
scenario inactive anyway and lists what to remove by hand. A component's
`uninstallScript` must exit non-zero when it leaves something behind.

### `labctl scenario render`

Prints a file from a scenario's directory with its template variables filled in,
exactly as the engine would apply it. Use it for a manifest the scenario leaves
to you, because `kubectl apply -f` on the raw file sends a literal
`{{.WorkloadName}}` to the cluster:

```bash
labctl scenario render node-drain-drill manifests/baseline.yaml --app go-api | kubectl apply -f -
```

It renders for `--app` when given, otherwise for the app the scenario was
activated with, and uses the activation's parameters (their defaults when the
scenario is not active). A path outside the scenario's directory is refused.

`scenario up` flags:

| Flag | Default | Meaning |
|---|---|---|
| `--deploy-prereqs` | off | install prerequisite platform components and build and deploy prerequisite apps that are not running yet. Each component installs as a recorded run, as `platform up <target>` does, so `platform teardown` removes it. Without it, a missing prerequisite stops `scenario up` with the command that installs it. |
| `--force` | off | reinstall even when the scenario is already active |
| `--set key=value` | - | override a scenario parameter; repeatable |

```bash
labctl scenario up autoscaling-under-load --set threshold=15 --set maxReplicas=4
```

Parameters a scenario accepts are declared in its `parameters` block - see the
[schema](../scenario-schema.md#parameters).

## Running several at once

Any number of scenarios can be active together, as long as the lab has room.
Before `scenario up` installs anything it checks the Docker engine's memory
against the baseline, everything already active and what the new scenario
adds; a Helm release or platform component that several scenarios share is
counted once. When there is not enough room it stops without changing the
cluster and prints:

- which active scenarios to bring down (`labctl scenario down <name>`) to make
  room, largest saving first, when that is enough; and
- how big to make Docker instead, with the resize command for colima, Docker
  Desktop, WSL or native Linux.

A scenario that needs more agent nodes than the cluster has gets them added
before it starts (k3d), and `scenario down` removes them again once no active
scenario needs them. An added node that holds a local volume stays, since its
data would go with it. A scenario marked `exclusive` runs alone. Two active
scenarios bound to the same app, or fewer CPUs than a scenario asks for, only
print a warning. The same check runs for `labctl incident inject` and from the
web UI. The fields scenarios use are in the
[scenario schema](../scenario-schema.md#requirements).

REST: `GET /api/v2/capacity` returns the Docker engine's CPUs and memory, the
memory in use and the share the check lets the lab plan to use, all in MiB; the
dashboard's Docker card shows it. It answers `404` on the incluster runtime and
`503` when Docker cannot be reached.

## Which application it runs against

A scenario names its workload through `{{.WorkloadName}}` rather than an app
([ADR-0014](../../adr/0014-workload-binding-and-app-contract.md)), so the same
scenario runs against any app that meets the contract:

```bash
labctl scenario up autoscaling-under-load --app java-api
```

The app is **recorded with the activation**. `verify` and `down` read it back, so
a scenario brought up against `java-api` is graded and torn down against
`java-api`: not against whatever `APP_NAME` happens to say in the shell you come
back to an hour later.

The UI asks the same question in its activation dialog, and marks any app that
does not declare a capability the scenario needs.

## `labctl scenario reset`

Fast retry without a lab teardown: deactivate the scenario and re-activate it,
both as recorded, cancellable runs. Because component installs are idempotent,
a retry converges in seconds rather than minutes.

```bash
labctl scenario reset observability-sre
```

## `labctl scenario verify`

Runs the scenario's `checks` and reports pass or fail for each. Exits non-zero
if anything fails, so it is safe in CI.

```bash
labctl scenario verify observability-sre
labctl scenario verify observability-sre --watch --interval 10s --timeout 5m
```

| Flag | Default | Meaning |
|---|---|---|
| `--watch` | off | re-run until everything passes or `--timeout` elapses |
| `--interval` | `10s` | delay between re-runs in watch mode |
| `--timeout` | `5m` | overall watch deadline |
| `--check-timeout` | `30s` | per-check timeout |
| `--record` | - | write what the lab answered the declared checks, the variables they were rendered with and each result, to this file (the last attempt under `--watch`); the scenario's replay tests read it ([TESTING](../../TESTING.md#real-cluster)) |

`promql` checks query Prometheus at `http://prometheus.<DOMAIN_SUFFIX>`;
override with `PROMETHEUS_URL`.

**Reading the output.** A check can declare a `remediation` hint and/or
`pending: true`.

- A failing check with a `remediation` prints it under **Next step(s)**. The
  generic "a pod may still be starting" hint now appears only for a genuine
  failure that has no remediation of its own.
- A failing check marked `pending` renders as **PENDING**, not **FAIL** - it
  means "you have not done this drill step yet". `verify` still exits non-zero,
  but without the alarming "checks failed" wording.

REST: `POST /api/v2/scenarios/{name}/verify` - synchronous, with the same 30s
per-check default as the CLI and the whole run bounded at 5 minutes. A check cut
off by that bound says so, rather than blaming its own `timeoutSeconds`. Use the
CLI's `--watch` for long convergence.

## `labctl scenario new`

Scaffolds `scenarios/<name>/` with a valid scenario file and one passing
readiness check, so it is green under `verify` immediately. `--force`
overwrites an existing directory.

```bash
labctl scenario new my-first-scenario
```

Walkthrough: [your first scenario](../../authoring/first-scenario.md).

## `labctl validate`

Loads and validates every declarative content item (scenarios, incidents,
learning paths and challenges) against the content model. It checks required
fields, cross-references (a path or challenge must point at content that
exists) and templates (an unknown variable is an error). Problems are reported
as `file:line: [kind/name] message`, and it exits non-zero if there are any.

```bash
labctl validate            # human-readable report
labctl validate --json     # machine-readable, for tooling
```

Extra content roots named in `SNOWOPS_CONTENT_PATH` are discovered and
validated alongside the in-repo content; a later root overrides an earlier one
on a name collision, so you can add or shadow content without forking. See
[R03](../../runbooks/R03-content-authoring-and-validation.md) and
[ADR-0009](../../adr/0009-content-validation-strategy.md).

## Multi-environment promotion

There is deliberately **no `labctl env promote`**. Promotion between the three
environments the `env-promotion` scenario deploys (`env-dev` → `env-staging` →
`env-prod`) uses the real Kubernetes commands a platform engineer uses on the
job. The point is to learn `kubectl` and rollouts, not a labctl verb. labctl
sets the scenario up and **grades** the result.

```bash
labctl scenario up env-promotion
bash scenarios/env-promotion/scripts/build-image.sh v1.1.0 --app go-api --namespace go-api
kubectl -n env-dev set image deployment/go-api go-api=go-api:v1.1.0
kubectl -n env-dev rollout status deployment/go-api
# ...promote the same image forward to env-staging, then env-prod...
labctl scenario verify env-promotion    # grades: declared tag == running image, everywhere
```

The scenario's `explore.commands` lists the full flow, including a
`kubectl rollout undo` rollback drill. Runbook:
[R06](../../runbooks/R06-multi-env-promotion.md).
