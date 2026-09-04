# Lifecycle & environment

Building the lab, checking the machine, and reading back what labctl did.

## Whole-lab commands

| Command | What it does |
|---|---|
| `labctl init` | Install tools, create the cluster, install platform components. Same as `make setup-tools && make runtime-up && make platform-up`. |
| `labctl teardown` | Deactivate scenarios and incidents, destroy apps, remove the platform, delete the cluster. |
| `labctl reset` | `teardown` followed by `init`. |
| `labctl status` | Cluster info, platform health and deployed apps in one view. |

## Preparing a machine

### `labctl doctor`

Verifies every external tool the lab depends on: installed, new enough, and the
cluster reachable. Each problem is reported with why it matters and how to fix
it. Exits non-zero when anything required is missing, so it is safe as a script
gate.

```bash
labctl doctor
```

### `labctl setup-tools`

Installs those tools, version-pinned from `config/versions.env`, for the active
`PROFILE`. This is the first step of `init`; run it alone to prepare a machine
without creating a cluster. Equivalent to `make setup-tools`.

### `labctl check`

Narrower probes than `doctor`, useful inside scripts.

```bash
labctl check tools      # required CLI tools are installed
labctl check cluster    # kubectl cluster-info succeeds
labctl check ingress    # the ingress controller is running and responding
```

### `labctl hosts`

Manages a labctl-owned block in `/etc/hosts` so cluster ingress hostnames
(`*.k3d.local`) resolve. The block is delimited and rewritten in place, so it is
safe to run repeatedly. Needs privileges to write `/etc/hosts`.

```bash
labctl hosts add        # add or refresh the managed block
labctl hosts remove     # remove it
```

## The cluster

`runtime` operates on the Kubernetes cluster directly; `lab` does the same
through the durable run engine, so the operation is recorded and cancellable.

```bash
labctl runtime up          # create the cluster from the configured profile
labctl runtime down        # destroy it
labctl runtime status      # connectivity and node info
```

```bash
labctl lab up              # create the cluster as a recorded, cancellable run
labctl lab down            # tear it down the same way
labctl lab status          # state from the run history — fast, no cluster calls
labctl lab status --live   # additionally probe the cluster for reachability
```

Which profile is used comes from `PROFILE` (`k3d`, `kind` or `incluster`); see
[runtime profiles](../../runtime-profiles.md).

## Snapshots and lab reset

A snapshot records **intent** — which platform components, apps and scenarios
are active — as a small YAML file in `.labctl/snapshots/`. It is not a copy of
cluster bytes. Restore replays the normal idempotent install paths.

```bash
labctl lab snapshot before-gameday    # record the current state
labctl lab snapshots                  # list saved snapshots
labctl lab restore before-gameday     # converge back to it
labctl lab delete before-gameday
labctl lab reset                      # tear back to post-init (interactive)
labctl lab reset --yes                # non-interactive
```

- **Snapshot sources** — platform components from labctl's install markers in
  `.labctl/platform/`, scenarios from the scenario engine's state, apps by live
  kubectl probe. Anything installed outside labctl is not tracked.
- **Restore order** — ingress, then monitoring, then the remaining platform
  components, then apps, then scenarios. Already-active pieces are skipped, so
  restoring over a half-converged lab is safe.
- **Reset** — stops traffic, deactivates all scenarios, destroys deployed apps
  and uninstalls platform components *except* the ingress category. It keeps
  going past individual failures and reports what stuck. The cluster stays up.

REST: `GET/POST/DELETE /api/v2/lab/snapshots[/{name}]`,
`POST /api/v2/lab/snapshots/{name}/restore` (async, returns a job id),
`POST /api/v2/lab/reset?confirm=true` (async; refuses without `confirm`).

## Reading back what happened

Every operation that shells out is recorded with its status, timing, exit code
and full output. Records survive restarts, so a run can be read long after it
finished.

```bash
labctl runs list                        # newest first, 20 by default
labctl runs list --limit 50
labctl runs list --kind platform.install
labctl runs list --status failed
labctl runs logs <run-id>               # the run's full output
labctl runs logs <run-id> --follow      # keep printing until the run ends
labctl runs cancel <run-id>             # cancel a queued or in-progress run
```

| Command | Flag | Default | Meaning |
|---|---|---|---|
| `runs list` | `--limit` | `20` | maximum runs to show |
| | `--kind` | — | filter by kind, e.g. `platform.install` |
| | `--status` | — | filter by status |
| `runs logs` | `-f, --follow` | off | stream new output until the run ends |

How the engine records and cancels work is in
[architecture §3](../../architecture/ARCHITECTURE.md) and
[R01](../../runbooks/R01-run-engine-and-cancellation.md).
