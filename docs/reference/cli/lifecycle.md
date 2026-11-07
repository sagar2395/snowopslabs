# Lifecycle & environment

Building the lab, checking the machine, and reading back what labctl did.

## Whole-lab commands

| Command | What it does |
|---|---|
| `labctl init` | Install tools, start Docker at the lab's size, check Docker's resources, create the cluster, install the platform, then check the cluster is healthy. A lab still on the old `*.k3d.local` or `*.kind.local` hostnames moves to `*.localhost` in place. Safe to re-run: it is also how the lab comes back after a reboot. |
| `labctl teardown` | Deactivate scenarios and incidents, destroy apps, remove the platform, delete the cluster. |
| `labctl reset` | `teardown` followed by `init`. |
| `labctl status` | Cluster info, platform health and deployed apps in one view. When the cluster is configured but not answering it says so, with the reason, instead of listing everything as not installed. On a k3d lab it also reports an inotify limit a Docker VM restart reset and nodes `NotReady` while their container is up, with the commands that recover them; it changes nothing itself. |

### What `init` checks

`init` fails loudly rather than reporting a lab that does not work:

- **Docker size.** On macOS, a stopped colima is started at `LAB_CPUS` /
  `LAB_MEMORY` (default 2 CPU / 4 GB) and never shrunk below its current size.
  A Docker engine that is already running below the 2 CPU / 4 GB minimum is
  refused, with the resize command for your setup (colima, Docker Desktop, WSL
  or native Linux). labctl does not resize a running engine itself, because
  that would stop your other containers.
- **Image downloads.** Cluster images are pulled up front with progress; a
  download that stalls is retried once and then reported, instead of hanging.
- **Platform.** If Prometheus or Grafana fails to install, `init` exits
  non-zero and lists what failed. Installs are safe to repeat.
- **Health.** `init` ends by checking the API server answers and every node is
  Ready. Only then does it print "Lab is up", with the lab's real URLs.
- **An existing lab is kept.** Re-running `init`, the way back after a reboot,
  never deletes the cluster. It restarts it in order if it is unhealthy, skips
  platform installs that are already deployed and waits for their pods to be
  Ready. If the cluster still cannot be reached it stops and suggests
  `labctl reset`, which rebuilds from scratch on purpose.
- **URLs and ports.** Lab hostnames are `<service>.<cluster>.localhost`, so each
  lab on a machine has its own (and its own browser cookies). When host ports
  80/443 are taken, the cluster's ingress falls back to free ones (8080/8443 and
  up). `~/.snowops/clusters/<name>.env` records the ports and the domain suffix
  the cluster was built with, and every URL labctl prints, the UI's links and
  every check use them. After the platform is up, `init` requests the lab's
  Grafana through that port from this machine; if something else answers (a port
  taken after the lab was built), a k3d lab gets free ports added to its load
  balancer (`k3d cluster edit --port-add`, no rebuild) and kind says how to
  change them. Checks reach lab hostnames on `127.0.0.1` directly, so grading
  needs no `/etc/hosts` entries and ignores `HTTP_PROXY`.
- **Dashboards have data.** `init` then asks Grafana to query its Prometheus
  datasource. If it cannot, `init` re-applies Grafana with the lab's current
  values (an existing lab otherwise keeps the values it was installed with) and
  checks again; if it still cannot, `init` exits with Grafana's reason. When
  Grafana refuses the admin password, `init` says so and skips the check: set
  `GRAFANA_ADMIN_PASSWORD` in `.env` to the password you chose.
- **Names.** A cluster name is unique on a machine, across k3d and kind; the
  defaults are `snowops` (k3d) and `snowops-kind` (kind).

## Preparing a machine

### `labctl doctor`

Verifies every external tool the lab depends on (installed and new enough) and
that Docker is running with at least the lab's minimum of 2 CPU / 4 GB. Each
problem is reported with why it matters and the exact command that fixes it on
your setup. Exits non-zero when anything required is missing, Docker is not
running, or Docker is too small, so it is safe as a script gate. A stopped
colima is only a note: `labctl init` starts it. When the lab's k3d or kind
cluster answers, a note also warns once the memory its pods request passes 85%
of Docker's memory, and names the command that stops a scenario. On k3d, a
note says when `fs.inotify.max_user_instances` is back below 512 after a Docker
VM restart; the next lab, platform, scenario, incident or traffic command
raises it.

On WSL2, `doctor` adds a dedicated **WSL notes** section. It checks whether the
lab directory is on a Windows mount (any path under `/mnt/`) where 9p bridge
slowness and CRLF line endings cause issues, reminds that Docker memory comes
from `%UserProfile%\.wslconfig`, confirms that default `*.localhost` lab URLs
open in your Windows browser with no hosts-file edits, explains that custom
domain suffixes require Windows hosts file entries (or `generateHosts=false` in
`/etc/wsl.conf`), and notes that closing every terminal stops the lab after an
idle timeout. See [Getting started on Windows (WSL2)](../../getting-started/wsl.md).

```bash
labctl doctor
```

### `labctl setup-tools`

Installs the tools the active `PROFILE` needs: kubectl, helm, and k3d or kind,
plus a Docker engine. This is the first step of `init`; run it alone to prepare
a machine without creating a cluster. Equivalent to `make setup-tools`.

A tool at or above its minimum in `config/versions.env` is left alone, so your
own newer copy is never replaced. Otherwise:

| OS | How tools are installed | Needs sudo? |
|---|---|---|
| macOS | Homebrew (`kubernetes-cli`, `helm`, `k3d`, `kind`, `colima`, `docker`, `docker-buildx`); an older brew install is upgraded | no (installing Homebrew itself asks once) |
| Linux, WSL2 | the pinned version from `versions.env`, downloaded into `~/.local/bin` (`SNOWOPS_BIN_DIR` overrides) | only for Docker Engine |

labctl puts `~/.local/bin` on its own PATH, so freshly installed tools work
straight away; setup-tools prints the line to add to your shell profile so
`kubectl` works in your own terminal too.

Docker on Linux and WSL:

- **Just added to the `docker` group:** the group only applies to new login
  sessions, so setup-tools stops and says so. Log out and back in (WSL:
  `wsl --shutdown` in PowerShell, then reopen), then re-run `labctl init`.
- **Docker Desktop installed on Windows but not enabled for this distro:**
  setup-tools does not install a second Docker; it tells you to turn on
  Settings → Resources → WSL Integration for the distro.
- **WSL without systemd:** Docker is started with `service`, and setup-tools
  explains how to enable systemd so it starts on its own.

### `labctl check`

Narrower probes than `doctor`, useful inside scripts.

```bash
labctl check tools      # required CLI tools are installed
labctl check cluster    # kubectl cluster-info succeeds
labctl check ingress    # the ingress controller is running and responding
```

### `labctl hosts`

Lab URLs use `<service>.<cluster>.localhost` (for example
`grafana.snowops.localhost`) by default, and every browser resolves
`*.localhost` to this machine, so a default lab needs no hosts entries and
`hosts add` says so and does nothing. It is for a lab with a `DOMAIN_SUFFIX`
you set. A lab built before the `.localhost` default (`*.k3d.local` or
`*.kind.local`) needs none either: `labctl init` moves it to
`<cluster>.localhost` in place, unless `DOMAIN_SUFFIX` is set in the
environment or `.env`. It manages a labctl-owned block in `/etc/hosts` so those hostnames
resolve; the block is delimited and rewritten in place, so it is safe to run
repeatedly.

```bash
labctl hosts add      # add or refresh the managed block; asks for sudo to write
labctl hosts remove   # remove it
```

Run it without `sudo`. It reads the cluster as you, then elevates only to write
the file; under `sudo` kubectl reads root's kubeconfig and finds no cluster.

The block lists three sources, sorted:

- the platform components' hostnames (`grafana`, `prometheus`, `alertmanager`,
  `opencost`, `argocd`, `traefik`, `dashboard`, `chaos`, `vault`), so a run
  straight after `labctl init` covers components not installed yet
- one hostname per app under `apps/`
- every Ingress host in the cluster under the domain suffix: the source of
  truth for hostnames a scenario adds, such as env-promotion's
  `<app>-dev`, `<app>-staging` and `<app>-prod`

A host outside the domain suffix is never written. If the cluster cannot be
read, the platform and app hostnames are still written and a warning says so.

On such a lab, `labctl scenario up` and `labctl app deploy` list any Ingress
hostname the managed block does not cover yet, so a new hostname is announced before a
browser fails to resolve it. Re-run `hosts add` when they do.

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
labctl lab status          # state from the run history - fast, no cluster calls
labctl lab status --live   # additionally probe the cluster for reachability
```

Which profile is used comes from `PROFILE` (`k3d`, `kind` or `incluster`); see
[runtime profiles](../../runtime-profiles.md).

## Snapshots and lab reset

A snapshot records **intent** (which platform components, apps and scenarios
are active) as a small YAML file in `~/.snowops/state/<cluster>/snapshots/`. It is not a copy of
cluster bytes. Restore replays the normal idempotent install paths.

```bash
labctl lab snapshot before-gameday    # record the current state
labctl lab snapshots                  # list saved snapshots
labctl lab restore before-gameday     # converge back to it
labctl lab delete before-gameday
labctl lab reset                      # tear back to post-init (interactive)
labctl lab reset --yes                # non-interactive
```

- **Snapshot sources**: platform components from labctl's install markers in
  `~/.snowops/state/<cluster>/platform/`, scenarios from the scenario engine's state, apps by live
  kubectl probe. Anything installed outside labctl is not tracked.
- **Restore order**: ingress, then monitoring, then the remaining platform
  components, then apps, then scenarios. Already-active pieces are skipped, so
  restoring over a half-converged lab is safe.
- **Reset**: stops traffic, deactivates all scenarios, destroys deployed apps
  and uninstalls platform components *except* the ingress category. It keeps
  going past individual failures and reports what stuck. The cluster stays up.
- **An unreachable cluster**: `snapshot` and `reset` read the cluster to learn
  what runs. When it does not answer they stop with `cannot reach the cluster`
  and change nothing: a snapshot taken then would record an empty lab, and a
  reset planned from it would skip everything it should undo.

REST: `GET/POST/DELETE /api/v2/lab/snapshots[/{name}]`,
`POST /api/v2/lab/snapshots/{name}/restore` (async, returns a job id),
`POST /api/v2/lab/reset?confirm=true` (async; refuses without `confirm`).
Taking a snapshot and resetting answer `503 unavailable` when the cluster does
not answer.

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
labctl runs recover <run-id>            # inspect interrupted work without changing it
labctl runs recover <run-id> --force    # explicitly release its reserved key
```

Ctrl-C (or SIGTERM) stops the command and the run it started: the script's
process group is sent SIGTERM so it can clean up, and the run is recorded as
`cancelled`. `runs cancel` and the web UI's **Cancel** record the cancel in
the run store; the process running the run reads it within a second and stops
the script the same way. `labctl ui` stops accepting connections and waits up to ten
seconds for requests in flight. `scenario verify --watch` stops between
attempts.

`Recovery required` means work started and its owner exited or its owner file is
missing. The saved lifecycle status is historical; the operation may still be
running. Its key and partial logs remain reserved. `runs logs --follow` drains the saved
output, prints recovery guidance, and exits non-zero. Ordinary cancellation
cannot release this key.

Inspect the lab before using `runs recover <run-id> --force`. Without `--force`,
the command shows the run, key and warning and changes nothing. A forced release
records the local UID, available user name, time, reason and former owner in one
transaction. It refuses a busy owner file or another probe error. An
`owner_missing` run can be released with `--force`; labctl adds a warning that it
cannot check whether this work is still running and that you must check the lab.
The audit retains `owner_missing` as its reason. Repeating the command shows
the existing release without replacing its audit data. A released run says
`Key released by operator`. This permits another operation on the same key;
it does not stop a surviving process or establish that cleanup finished.

The separate remote-cancellation path still records `cancelled` before execution
and cleanup return. Owner recovery does not fix that ordering. Do not treat a
remote cancellation as proof that another same-key operation is safe. Stored
cancellation requests remain planned in [the recovery design](../../rfcs/0002-owned-runs-and-cancellation.md#phase-2-store-cancellation-requests).

| Command | Flag | Default | Meaning |
|---|---|---|---|
| `runs list` | `--limit` | `20` | maximum runs to show |
| | `--kind` | - | filter by kind, e.g. `platform.install` |
| | `--status` | - | filter by status |
| `runs logs` | `-f, --follow` | off | stream new output until the run ends |
| `runs recover` | `--force` | off | release an interrupted key after warning that work may still be running |

### Run API recovery fields

`GET /api/v2/runs`, `GET /api/v2/runs/{id}` and
`GET /api/v2/runs/{id}/logs?after=<seq>` add optional `recoveryState`,
`recoveryReason`, `recoveryCommand`, `ownerId`, `releasedAt` and `releasedBy`.
`recoveryState=required` keeps the key; `released` records an operator override.
The owner ID remains the former engine's ID after release. The release timestamp
and actor are omitted before release.

The log response's `done` contract is unchanged: true only for a terminal status
with no lines after the cursor. A recovery-aware follower reads until an empty
page with `recoveryState=required`, then stops with recovery guidance even though
`done` remains false. Clients that ignore recovery fields can show only the last
saved lifecycle status and keep polling. The Runs screen shows the recovery
state, saved steps and release audit, and hides advancing duration and ordinary
cancellation for interrupted work. `POST /api/v2/runs/{id}/cancel` returns
`409 recovery_required` with the recovery command for that work. Key release is
available through the local CLI, not a new REST mutation.

### Upgrade the run store offline

Stop every old labctl process, including servers and workers. Back up the
database in `SNOWOPS_HOME` and its associated files after stopping those
processes. Upgrade all entry points, then open the store to migrate before
restarting work. Mixed-version and online upgrades are unsupported.

The store holds a shared upgrade lock for its lifetime. A pending migration
tries the exclusive lock without waiting and checks recorded active owners.
A busy, unreadable or invalid owner file refuses the migration. A missing owner
file allows the offline migration because the exclusive upgrade lock excludes
open stores and retained workers. Existing recovery states, keys and logs stay
unchanged. Legacy active
rows without an owner become recovery required and retain their keys and logs;
use the same explicit recovery command after checking the lab. The first upgrade
cannot detect an old binary that never took these locks. Stopping those processes
is an operator prerequisite, not a property the migration can prove.

Owner files live beside the canonical database path in `<database>.owners`.
Do not delete that directory or its referenced files. A missing owner file lets
startup continue: that owner's queued work is cancelled, while running work keeps
its key and logs as `required` / `owner_missing`. Other owners and keys continue.
Inspect the lab before explicitly releasing that work; do not recreate a missing
file to bypass recovery. Startup removes only unlocked files that no unfinished
or recovery-required run names, holding their lock during deletion.
Use local storage on one host: macOS, Linux or WSL2 in its Linux home. Remote
filesystems, hard-link aliases and external owner-file replacement are unsupported.

How the engine records and cancels work is in
[architecture §3](../../architecture/ARCHITECTURE.md) and
[R01](../../runbooks/R01-run-engine-and-cancellation.md).
