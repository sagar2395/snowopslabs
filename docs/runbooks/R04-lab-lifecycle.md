# R04 — Lab lifecycle reliability

**Wave:** W3 (reliability-first slice) · **Time:** ~20 minutes · **Cluster needed:** yes (k3d)

Before first delivery, the one thing a reviewer must be able to trust is the
core loop: bring a lab up, and tear it down cleanly — repeatedly, and after an
interruption — without it hanging or wedging. This runbook proves those
properties end-to-end on a real cluster. The script-level guarantees behind them
are also enforced hermetically in CI (`test/shell/platform_uninstall.bats`,
`platform_install.bats`, `runtime_lifecycle.bats`).

> Deferred to a later pass (with the durable-engine migration, W3-T01/T03/T04):
> store-backed component tracking, `lab down` reporting exactly what it could
> not remove, and `lab status` served from the store in <200ms. This runbook
> covers the reliability slice shipped for first delivery.

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

## Sign-off

| Step | Result | Notes |
|---|---|---|
| 1. Clean bring-up | | |
| 2. Re-run init converges (fast, no errors) | | |
| 3. Teardown completes, no hang | | |
| 4. Teardown of down lab is a no-op | | |
| 5. Reset round-trips | | |

Time to teardown (step 3): _____   ·   Any step that felt slow or risky: _____
