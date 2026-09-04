# Scenarios

Scenarios are declarative labs: a YAML file naming the platform components,
apps and dashboards to install, plus machine-verifiable checks that grade your
work. The schema is [scenario schema](../scenario-schema.md); the catalog of
what ships is [scenarios](../../scenarios.md).

## Browsing and running

```bash
labctl scenario list                        # names, categories, active state
labctl scenario info observability-sre      # description, prerequisites, components, hints
labctl scenario up observability-sre        # activate: install every declared component
labctl scenario down observability-sre      # deactivate: remove them
labctl scenario status                      # which scenarios are active
```

`scenario up` flags:

| Flag | Default | Meaning |
|---|---|---|
| `--deploy-prereqs` | off | build and deploy prerequisite apps that are not running yet |
| `--force` | off | reinstall even when the scenario is already active |
| `--set key=value` | — | override a scenario parameter; repeatable |

```bash
labctl scenario up autoscaling-under-load --set threshold=15 --set maxReplicas=4
```

Parameters a scenario accepts are declared in its `parameters` block — see the
[schema](../scenario-schema.md#parameters).

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

`promql` checks query Prometheus at `http://prometheus.<DOMAIN_SUFFIX>`;
override with `PROMETHEUS_URL`.

**Reading the output.** A check can declare a `remediation` hint and/or
`pending: true`.

- A failing check with a `remediation` prints it under **Next step(s)**. The
  generic "a pod may still be starting" hint now appears only for a genuine
  failure that has no remediation of its own.
- A failing check marked `pending` renders as **PENDING**, not **FAIL** — it
  means "you have not done this drill step yet". `verify` still exits non-zero,
  but without the alarming "checks failed" wording.

REST: `POST /api/v2/scenarios/{name}/verify` — synchronous and bounded to about
12 seconds. Use the CLI's `--watch` for long convergence.

## `labctl scenario new`

Scaffolds `scenarios/<name>/` with a valid scenario file and one passing
readiness check, so it is green under `verify` immediately. `--force`
overwrites an existing directory.

```bash
labctl scenario new my-first-scenario
```

Walkthrough: [your first scenario](../../authoring/first-scenario.md).

## `labctl validate`

Loads and validates every declarative content item — scenarios, incidents,
learning paths and challenges — against the content model. It checks required
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
bash scenarios/env-promotion/scripts/build-image.sh v1.1.0
kubectl -n env-dev set image deployment/go-api go-api=go-api:v1.1.0
kubectl -n env-dev rollout status deployment/go-api
# ...promote the same image forward to env-staging, then env-prod...
labctl scenario verify env-promotion    # grades: declared tag == running image, everywhere
```

The scenario's `explore.commands` lists the full flow, including a
`kubectl rollout undo` rollback drill. Runbook:
[R06](../../runbooks/R06-multi-env-promotion.md).
