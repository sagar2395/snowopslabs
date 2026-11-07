# R04: Lab lifecycle reliability

**Wave:** W3 (reliability-first slice) · **Time:** ~20 minutes · **Cluster needed:** yes (k3d)

Before first delivery, the one thing a reviewer must be able to trust is the
core loop: bring a lab up, and tear it down cleanly (repeatedly, and after an
interruption) without it hanging or wedging. This runbook proves those
properties end-to-end on a real cluster. The script-level guarantees behind them
are also enforced hermetically in CI (`test/shell/platform_uninstall.bats`,
`platform_install.bats`, `runtime_lifecycle.bats`).

> **Update (W3-T01/T08):** the durable lab service (`internal/service/lab`) now
> runs bring-up/teardown/status through the recorded, cancellable run engine,
> and `labctl lab up|down|status` (W3-T08) drives it - see steps 6–7 below. The
> web run console reads the same runs (W6-T05). Still deferred: store-backed
> component tracking and exact "what could not be removed" teardown (W3-T04),
> and porting `labctl platform` onto a platform service (W3-T03). Steps 1–5
> continue to exercise the legacy `make` path, which remains the default entry
> point (`make init`/`teardown`) until it is switched over.

---

## Preconditions

- Docker/Colima with ≥2 CPU / 4 GB. `labctl init` starts a stopped colima at
  that size and refuses a running engine that is smaller.
- `bin/labctl` built (`make cli-build`), or use `make` targets directly.
- `PROFILE=k3d` (the default).

---

## 1. A clean bring-up

```bash
$ make init
```

**Expect:** cluster created, platform stack installed, exit 0. Note the elapsed
time and that pods settle (`kubectl get pods -A`).

---

## 2. Re-running `init` converges (idempotency)

Run it again on the already-up lab:

```bash
$ make init
```

**Expect:** exit 0, and it is *fast* - the cluster is skipped
(`Cluster '…' already exists, skipping creation.`) and every Helm release is
`upgrade --install`, so nothing is recreated or errors. This is the property a
reviewer relies on after a Ctrl-C or a flaky step.

---

## 3. Teardown completes and never hangs

```bash
$ time make teardown
```

**Expect:** exit 0 within a couple of minutes. Watch for the failure mode this
slice fixed: **no step should sit forever on `kubectl delete namespace`.** Even
if a namespace is slow to terminate, each delete is bounded to 60s and the k3d
cluster deletion removes everything as the backstop.

**Reviewer check:** `k3d cluster list` no longer shows the cluster; `docker ps`
shows no stray k3d containers.

---

## 4. Teardown of an already-down lab is a safe no-op

```bash
$ make teardown ; echo "exit=$?"
```

**Expect:** `exit=0`. `runtime down` reports the cluster is not found and deletes
nothing; nothing errors. Re-running teardown must always be safe.

---

## 5. Reset (teardown + init) round-trips

```bash
$ make reset
```

**Expect:** a full teardown followed by a clean bring-up, exit 0 - proving the
loop is repeatable, which is exactly what a reviewer doing multiple sessions
will do.

---

## 6. Durable lab service (W3-T01)

The cluster lifecycle now has a service on the run engine
(`src/internal/service/lab`). It is the root of the W3/W4 service layer and
unblocks the W6-T05 run console. Its guarantees are proven hermetically (no
cluster, no network, no real binaries) with `toolchain.Fake`:

```bash
$ cd src && go test -race ./internal/service/lab/
```

**Expect:** `ok`, race-clean. The suite proves the properties this runbook
verifies by hand at the script level, now at the service level:

- **`Up`/`Down` are recorded runs** under the exclusive `lab` lock key, with the
  cluster name passed as argv and the right per-kind timeout.
- **Conflicting operations are refused, not raced**: a second lab op while one
  is in flight returns `*run.LockConflictError` naming the holder (the 409).
- **A cancelled `lab up` leaves nothing in flight and frees the lock**: the run
  reaches a terminal `cancelled` state (the engine cancels the whole process
  group, ADR-0003) and a fresh operation is immediately accepted.
- **`lab status` is answered from the store** (well under the 200ms budget) and
  degrades to `unknown`/`error`/`provisioning`/`up`/`down` from the run history;
  `--live` additionally attaches a cluster probe without ever failing the read.

**Reviewer check:** the run history the service writes is visible in the same
store the CLI reads: `labctl runs list` shows `lab.up`/`lab.down` entries with
status, duration, and logs.

---

## 7. `labctl lab` on the durable engine (W3-T08)

The CLI now drives the service, so cluster lifecycle is a recorded, cancellable,
followable run instead of a fire-and-forget shell-out:

```bash
$ labctl lab status                 # from the store, no cluster round-trip
$ labctl lab up                     # provisions the configured profile; follows the transcript
$ labctl lab status --live          # adds a real kubectl reachability probe
$ labctl runs list                  # the lab.up run is recorded with timing + logs
$ labctl lab down                   # tears down as a recorded run
```

**Expect:** `lab up`/`down` stream their output and exit non-zero if the run
fails; `lab status` answers instantly from the store (`unknown` before the first
run). The same runs appear in `labctl runs list|logs` and in the web run console
(`labctl ui` → **Runs**). Hermetic coverage:
`go test ./internal/cli/ -run TestLab` and `go test ./internal/httpapi/ -run TestHandleRun`.

---

## 8. Replacing a node in a running lab

`k3d node create` is not a drop-in for a node the cluster already had. Two
traps bite every time, and both look like "the lab is broken" rather than like a
cause. `scenarios/cluster-upgrade-drill/scripts/roll-node.sh` handles both; any
other code path that adds a node must too.

**The new node never goes Ready - containerd's CRI plugin failed to load.**
The colima/Docker VM defaults `fs.inotify.max_user_instances` to 128, which is
too low for containerd's CNI watcher. The node comes up, k3s runs, and
`containerd.log` says:

```
failed to create CRI service: failed to create cni conf monitor for default:
failed to create fsnotify watcher: too many open files
```

`runtimes/k3d/up.sh` raises the limit to 512 via `raise_inotify_limits` at
cluster-create time only, so a node created later never gets it. Raise the
sysctl on the cluster's node containers, then restart the new node - containerd
has already failed by the time the sysctl lands:

```sh
for n in $(docker ps --filter "label=k3d.cluster=snowops" --format '{{.Names}}'); do
  docker exec "$n" sysctl -w fs.inotify.max_user_instances=512
done
docker restart <new-node>
```

**The app lands in ImagePullBackOff on the new node.** Locally built lab images
(`go-api`, `echo-server`) are side-loaded with `k3d image import` at deploy time
(`src/engine/deploy.sh`) and exist in no registry. A fresh node has an empty
image store, so re-import after it joins. Import every built tag, not only the
one running: scenarios use others (backup-restore-drill's `data-writer` runs
`go-api:latest` with `imagePullPolicy: Never`, env-promotion runs `v1.1.0`), and
a pod that lands on the new node with its tag missing waits in
`ErrImageNeverPull` for good. `runtimes/k3d/add-agents.sh` and
cluster-upgrade-drill's `roll-node.sh` both import the whole set
(`runtimes/_lib/lab-images.sh`); by hand:

```sh
k3d image import $(docker images --format '{{.Repository}}:{{.Tag}}' go-api | grep -v '<none>') -c snowops
```

**Deleting a node destroys the local-path volumes pinned to it.** local-path PVs
carry node affinity. When the node goes, the data goes with it, but the PV and
its claim survive, so the pod sits `Pending` forever with "didn't match
PersistentVolume's node affinity". In this lab that routinely strands
Prometheus, Alertmanager and Loki, which means a scenario graded from
Prometheus can no longer be graded at all. Release the dead claims so their
StatefulSets rebind:

```sh
DRY_RUN=true bash scenarios/cluster-upgrade-drill/scripts/reclaim-stranded-pvcs.sh
bash scenarios/cluster-upgrade-drill/scripts/reclaim-stranded-pvcs.sh
```

**A deleted agent stays in the load balancer.** k3d keeps the upstream list of
`k3d-<lab>-serverlb` (`/etc/confd/values.yaml`) up to date for servers only, so
an agent that is deleted, or replaced under a new name, stays listed. Nothing
shows until the next Docker restart: nginx then fails with `host not found in
upstream`, the load balancer restart-loops, and the API server behind it is
unreachable too. `serverlb_repair` (`runtimes/_lib/k3d-serverlb.sh`) replaces
every name of a missing container with the live nodes of the same role and
restarts the load balancer. `roll-node.sh` and `runtimes/k3d/remove-agents.sh`
run it after deleting a node, and `labctl init` runs it on an existing cluster
(section 9), so a lab already in that state comes back without `labctl reset`.

Also note that `k3d node create <name>` does not produce a node called `<name>`:
k3d prefixes `k3d-` and appends its own index, so the created node must be
discovered by diffing `kubectl get nodes` rather than assumed.

---

## 9. The lab after the host restarts

Stopping and restarting Docker (on macOS: restarting the laptop, which stops
colima) leaves the cluster in a state that looks like a broken lab and never
recovers on its own. `kubectl get nodes` shows the agents, sometimes the server
too, stuck `NotReady` with "Kubelet stopped posting node status", while
`docker ps` shows every node container `Up`.

The cause is one line in each node's log:

```
level=error msg="Shutdown request received: failed to start networking: unable
to initialize network policy controller: error getting node subnet: failed to
find interface with specified node ip"
```

Docker hands the node containers their bridge addresses in whatever order they
start, so after a restart the nodes have swapped IPs. k3s resolves the node's
recorded address, finds no local interface holding it, and exits. It exits into
k3d's entrypoint, which has already moved on to `until kubectl uncordon
"$HOSTNAME"; do sleep 3; done` - a loop that can never succeed on a node with no
kubeconfig. The container therefore stays `Up` with no k3s inside it, Docker's
restart policy never fires, and the node stays `NotReady` until someone
restarts the container by hand. The second start works: by then the Node object
carries the address the container actually has.

Restarting one node at a time is not enough. Docker brings every node back at
once, so an agent can start while its server is still starting, and after the
restart a node's container may hold a different address from the one its Node
object records. k3s's network policy controller reads the recorded address
before the kubelet can correct it, so that node shuts down on every restart.

`runtimes/k3d/up.sh` handles all of it, so `labctl init` / `make init` is the
recovery:

1. It first repairs a load balancer whose config names a node that no longer
   exists (section 8), which no restart can fix. If the cluster is then not
   healthy (API not answering, a node container with no k3s, or a node not
   Ready), it restarts the cluster **in order** with
   `k3d cluster stop` + `k3d cluster start`: servers first, then agents.
2. While k3d starts it, the script compares each node container's IP with its
   Node's recorded `InternalIP`, running `kubectl` inside the server so it needs
   neither the load balancer nor the host kubeconfig. A stale record is
   corrected in place (a status patch; same Node, same name, same pods). This
   covers the server too: with a stale address the server's k3s shuts down
   every few seconds, so its API answers only in brief windows, and the check
   repeats until one pass finds every record right.
3. It then requires every node to run k3s and be Ready. Just after node
   containers (re)start it takes six samples in a row, 10 s apart, since a node
   can die a minute in while its Node still reads Ready; a lab that has been up
   for a while needs one look. A node that is still unhealthy has its address
   checked again and is restarted, at most twice.
4. It **never deletes the cluster**. If it still cannot be brought back it stops
   and points at `labctl reset`, which rebuilds from scratch on purpose.

`labctl init` then skips platform installs that are already deployed and waits
for their pods to be Ready. Because a skipped install never picks up changed
values, init's closing checks then ask Grafana to query Prometheus
(`/api/datasources/uid/prometheus/health`) and re-apply Grafana when it cannot;
if it still cannot, init stops with Grafana's reason instead of "Lab is up". Measured on colima (2 CPU / 4 GB, 3 nodes, with
go-api and observability-sre active): three consecutive `colima stop` → `labctl
init` cycles each recovered in about two minutes with the app, the scenario and
every pod intact.

Detecting a dead node by hand is the same test the script makes:

```sh
docker top k3d-snowops-agent-0 | grep /bin/k3s || docker restart k3d-snowops-agent-0
```

`kind` is not affected: its nodes run kubelet under systemd, which restarts it.

### The inotify limit after a Docker VM restart

A VM restart also puts `fs.inotify.max_user_instances` back at the VM's
default (128 on colima). `runtimes/k3d/up.sh` raises it to 512, but a lab left
running after the restart keeps the default, and a node's k3s later dies with
`failed to create inotify fd: too many open files`. Its container stays `Up`,
the node goes `NotReady`, its pods sit in `Terminating` and their replacements
stay `Pending`.

- Every labctl command that runs scripts against a k3d lab (lab, platform,
  scenario, incident, traffic) raises the limit back to 512 before its scripts
  run, and says so.
- `labctl status` only reports: a limit below 512, and each node that is
  `NotReady` while its container runs, naming "too many open files" when the
  node's recent log holds it. It prints the recovery: the sysctl loop when the
  limit is still low, then `docker restart <node>` for each such node.
- `labctl doctor` adds a note when the limit is below 512.

A node that died before the limit came back stays `NotReady` until its
container restarts; raising the limit alone does not revive it.

---

## Sign-off

| Step | Result | Notes |
|---|---|---|
| 1. Clean bring-up | | |
| 2. Re-run init converges (fast, no errors) | | |
| 3. Teardown completes, no hang | | |
| 4. Teardown of down lab is a no-op | | |
| 5. Reset round-trips | | |
| 6. `go test ./internal/service/lab/` green (durable service) | | |
| 7. `labctl lab up/status/down` recorded + followable (W3-T08) | | |

Time to teardown (step 3): _____   ·   Any step that felt slow or risky: _____
