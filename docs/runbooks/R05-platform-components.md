# R05 — Platform components

**Wave:** W3 · **Time:** ~15 minutes · **Cluster needed:** yes (k3d), for §2–4

Platform components (ingress, monitoring, mesh, …) are the layers a scenario
builds on. This runbook proves that installing, uninstalling, and checking a
component is reliable and — with the durable-engine migration (W3-T03) — that
each operation is a recorded, cancellable run scoped to *that* component, so two
components never race and a component's state is answerable from history.

> **Status (W3-T03/T04/T08):** the durable platform service
> (`internal/service/platform`) is in place and proven hermetically (§1).
> Single-target `labctl platform up|down <target>` run through the durable engine
> (recorded, cancellable), and every successful install/uninstall updates a
> store-backed **component inventory** (W3-T04). `labctl platform teardown`
> removes exactly what the inventory records and reports anything it could not
> remove (§5). Still legacy: bulk (no-arg) `up`/`down` and `status --live`.

---

## Preconditions

- Docker/Colima running with ≥4 CPU / 8 GB.
- `bin/labctl` built (`make cli-build`), or `make` targets available.
- For §2–4, a cluster up: `make init` or `labctl lab up`.

---

## 1. Durable platform service (W3-T03)

The per-component guarantees are enforced hermetically — no cluster, no network,
no real helm/kubectl — with `toolchain.Fake`:

```bash
$ cd src && go test -race ./internal/service/platform/
```

**Expect:** `ok`, race-clean. The suite proves:

- **Install/uninstall/status are recorded runs** with the right kind
  (`platform.install` / `platform.uninstall` / `platform.status`), the component
  as the run target, and the component scripts executed.
- **The exclusive lock is per component.** Installing and uninstalling the *same*
  provider at once is refused with a lock conflict; two *different* components
  install concurrently.
- **A status probe takes no lock** and never changes the derived install state —
  so you can always check a component, even mid-install.
- **A cancelled install terminates and frees the component lock**, so a retry is
  accepted immediately (the engine cancels the whole process group, ADR-0003).
- **State is answered from the store**, per component: `unknown` → `installing` →
  `installed` → `removing` → `removed`, or `error` on a failed op — derived from
  that component's own run history, never leaking across components.

---

## 2. Install one component ⚠️

Pick a cheap, self-contained component (ingress is already present after
`init`; use `mesh` or `data/kafka` for a clean install):

```bash
$ labctl platform up data/kafka
```

**Expect:** the install streams to completion, exit 0. Re-running it converges
(helm `upgrade --install`), not errors — the idempotency property scenarios rely
on.

**Reviewer check:** `kubectl get ns kafka` (or the provider's namespace) exists
and its pods settle.

---

## 3. Status reflects reality 🔍

```bash
$ labctl platform status data/kafka
```

**Expect:** the component's `status.sh` reports it healthy. A component that is
not installed reports absent, not an error.

---

## 4. Uninstall is clean and re-runnable ⚠️

```bash
$ labctl platform down data/kafka
$ labctl platform down data/kafka   # again — must be safe
```

**Expect:** the first uninstall removes the component; the second is a no-op that
exits 0 (uninstalls are guarded against "already gone"). No stray namespace or
release is left behind.

---

## 5. Exact teardown from the inventory (W3-T04) ⚠️

Every install/uninstall you ran above was recorded in the store's component
inventory. `platform status` (no `--live`) shows it; `platform teardown` acts on
it:

```bash
$ labctl platform status          # store-derived: what labctl has installed
$ labctl platform teardown        # uninstall exactly that, reporting failures
```

**Expect:** `teardown` uninstalls each recorded component (reverse order) as a
streamed run, then prints `Removed N of M`. If a component's `uninstall.sh` fails
it is named under **Could not remove** and teardown exits non-zero — the whole
point of W3-T04: no silent `exit 0` that leaves residue behind. Components
installed outside labctl are not in the inventory and are left untouched.

The store-backed inventory + finish-hook recorder are proven hermetically:

```bash
$ cd src && go test -race ./internal/inventory/ ./internal/store/ -run Component
```

---

## Sign-off

| Step | Result | Notes |
|---|---|---|
| 1. `go test ./internal/service/platform/` green (durable service) | | |
| 2. Component installs and re-run converges | | |
| 3. Status reflects installed/absent correctly | | |
| 4. Uninstall clean and safe to re-run | | |
| 5. `platform teardown` removes recorded set + reports failures (W3-T04) | | |

Any component that felt slow or left residue: _____
