# R04 — Lab lifecycle reliability

**Wave:** W3 (reliability-first slice) · **Time:** ~20 minutes · **Cluster needed:** yes (k3d)

Before first delivery, the one thing a reviewer must be able to trust is the
core loop: bring a lab up, and tear it down cleanly — repeatedly, and after an
interruption — without it hanging or wedging. This runbook proves those
properties end-to-end on a real cluster. The script-level guarantees behind them
are also enforced hermetically in CI (`test/shell/platform_uninstall.bats`,
`platform_install.bats`, `runtime_lifecycle.bats`).

> **Update (W3-T01/T08):** the durable lab service (`internal/service/lab`) now
> runs bring-up/teardown/status through the recorded, cancellable run engine,
> and `labctl lab up|down|status` (W3-T08) drives it — see steps 6–7 below. The
> web run console reads the same runs (W6-T05). Still deferred: store-backed
> component tracking and exact "what could not be removed" teardown (W3-T04),
> and porting `labctl platform` onto a platform service (W3-T03). Steps 1–5
> continue to exercise the legacy `make` path, which remains the default entry
> point (`make init`/`teardown`) until it is switched over.

---

## Preconditions

- Docker/Colima running with ≥4 CPU / 8 GB (see the README; the full stack needs
  it).
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

**Expect:** exit 0, and it is *fast* — the cluster is skipped
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

**Expect:** a full teardown followed by a clean bring-up, exit 0 — proving the
loop is repeatable, which is exactly what a reviewer doing multiple sessions
will do.

---

## 6. Durable lab service (W3-T01)

The cluster lifecycle now has a service on the run engine
(`src/internal/service/lab`). It is the root of the W3/W4 service layer and
unblocks the W6-T05 run console. Its guarantees are proven hermetically — no
cluster, no network, no real binaries — with `toolchain.Fake`:

```bash
$ cd src && go test -race ./internal/service/lab/
```

**Expect:** `ok`, race-clean. The suite proves the properties this runbook
verifies by hand at the script level, now at the service level:

- **`Up`/`Down` are recorded runs** under the exclusive `lab` lock key, with the
  cluster name passed as argv and the right per-kind timeout.
- **Conflicting operations are refused, not raced** — a second lab op while one
  is in flight returns `*run.LockConflictError` naming the holder (the 409).
- **A cancelled `lab up` leaves nothing in flight and frees the lock** — the run
  reaches a terminal `cancelled` state (the engine cancels the whole process
  group, ADR-0003) and a fresh operation is immediately accepted.
- **`lab status` is answered from the store** (well under the 200ms budget) and
  degrades to `unknown`/`error`/`provisioning`/`up`/`down` from the run history;
  `--live` additionally attaches a cluster probe without ever failing the read.

**Reviewer check:** the run history the service writes is visible in the same
store the CLI reads — `labctl runs list` shows `lab.up`/`lab.down` entries with
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

**The new node never goes Ready — containerd's CRI plugin failed to load.**
The colima/Docker VM defaults `fs.inotify.max_user_instances` to 128, which is
too low for containerd's CNI watcher. The node comes up, k3s runs, and
`containerd.log` says:

```
failed to create CRI service: failed to create cni conf monitor for default:
failed to create fsnotify watcher: too many open files
```

`runtimes/k3d/up.sh` raises the limit to 512 via `raise_inotify_limits` at
cluster-create time only, so a node created later never gets it. Raise the
sysctl on the cluster's node containers, then restart the new node — containerd
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
image store, so re-import after it joins:

```sh
k3d image import go-api:v1.2.0 -c snowops
```

**Deleting a node destroys the local-path volumes pinned to it.** local-path PVs
carry node affinity. When the node goes, the data goes with it, but the PV and
its claim survive, so the pod sits `Pending` forever with "didn't match
PersistentVolume's node affinity". In this lab that routinely strands
Prometheus, Alertmanager and Loki — which means a scenario graded from
Prometheus can no longer be graded at all. Release the dead claims so their
StatefulSets rebind:

```sh
DRY_RUN=true bash scenarios/cluster-upgrade-drill/scripts/reclaim-stranded-pvcs.sh
bash scenarios/cluster-upgrade-drill/scripts/reclaim-stranded-pvcs.sh
```

Also note that `k3d node create <name>` does not produce a node called `<name>`:
k3d prefixes `k3d-` and appends its own index, so the created node must be
discovered by diffing `kubectl get nodes` rather than assumed.

---

## 9. The lab after the host restarts

Stopping and restarting Docker (on macOS: restarting the laptop, which stops
colima) leaves the cluster in a state that looks like a broken lab and never
recovers on its own. `kubectl get nodes` shows the agents — sometimes the server
too — stuck `NotReady` with "Kubelet stopped posting node status", while
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
"$HOSTNAME"; do sleep 3; done` — a loop that can never succeed on a node with no
kubeconfig. The container therefore stays `Up` with no k3s inside it, Docker's
restart policy never fires, and the node stays `NotReady` until someone
restarts the container by hand. The second start works: by then the Node object
carries the address the container actually has.

`runtimes/k3d/up.sh` does this itself (`restart_dead_nodes`), so `labctl init` /
`make init` is the recovery — it restarts exactly the node containers with no
k3s process and waits for every node to report Ready. This runs *before* the
reachability probe on purpose: a dead server node would otherwise read as an
unreachable cluster and be deleted along with the whole lab.

Detecting it by hand is the same test the script makes:

```sh
docker top k3d-snowops-agent-0 | grep /bin/k3s || docker restart k3d-snowops-agent-0
```

`kind` is not affected: its nodes run kubelet under systemd, which restarts it.

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
