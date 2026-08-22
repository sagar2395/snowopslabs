# Runtime profiles

A **runtime profile** is how SnowOps Labs provisions the Kubernetes cluster a lab
runs on. v2 ships three, and only three (cloud runtimes were cut — see
[ADR-0001](adr/0001-cut-cloud-and-commercial-scope.md)):

| Profile | What it is | Use it for |
|---|---|---|
| `k3d` | k3s in Docker, multi-node, ports mapped to localhost | The default. Fast local laptop cluster; the whole demo loop. |
| `kind` | Kubernetes in Docker | CI and cross-checking; the nightly e2e job runs here. |
| `incluster` | No cluster of its own — targets the cluster `labctl` already runs in | Team/server mode, where SnowOps Labs is deployed *into* a cluster. |

Select a profile with `PROFILE=<name>` (env or `.env`), e.g.
`make init PROFILE=kind` or `labctl --project-dir . runtime up` after setting
`PROFILE`.

---

## The profile contract

Each profile is a directory `runtimes/<name>/` containing exactly:

| File | Required | Contract |
|---|---|---|
| `up.sh` | yes | Provision the cluster. **Idempotent**: if the cluster already exists, skip creation and exit 0. Must select the kube-context on success. |
| `down.sh` | yes | Tear the cluster down. **Idempotent no-op** when the cluster is already absent (exit 0, delete nothing). |
| `runtime.env` | yes | Profile-specific defaults, `KEY=value` lines, read by `labctl` and the platform scripts. |

`internal/runtime` discovers a profile by the presence of `up.sh`; a directory
without it is not a runtime. `up.sh`/`down.sh` are run through the executor from
the project root and receive configuration through the environment (golden rule
3) — they must not source `.env` themselves.

### Script guarantees (verified by `test/shell/runtime_lifecycle.bats`)

- `up.sh` **skips** creation when the cluster exists (`refute` cluster-create on
  a second run) and **creates** it when absent.
- `down.sh` is a **clean no-op** when the cluster is already gone (`refute`
  cluster-delete) — so re-running teardown, or tearing down a lab that never
  came up, never errors.

### runtime.env keys

Every profile defines the same keys so downstream scripts can rely on them:

| Key | k3d | kind | incluster | Meaning |
|---|---|---|---|---|
| `INGRESS_CLASS` | `traefik` | `nginx` | `traefik` | Ingress controller the platform installs and routes through. |
| `STORAGE_CLASS` | `local-path` | `standard` | `standard` | Default `StorageClass` for PVCs. |
| `DOMAIN_SUFFIX` | `k3d.local` | `kind.local` | `cluster.local` | Host suffix for ingress routes; content templates read `{{.DomainSuffix}}`. |
| `REGISTRY_TYPE` | `k3d-import` | `kind-load` | `none` | How locally-built app images reach the cluster. |

A new profile is added by creating `runtimes/<name>/` with these three files and
honouring the guarantees above — no Go change is needed (golden rule 2).

---

## Teardown ordering & safety

`make teardown` runs, in order: destroy apps → `platform down` → `runtime down`.
For `k3d`/`kind`, `runtime down` deletes the whole cluster, so it is the real
backstop — every platform `uninstall.sh` bounds its `kubectl delete namespace`
with `--timeout` so a namespace stuck `Terminating` can never block teardown
before the cluster deletion runs (guarded by
`test/shell/platform_uninstall.bats`). For `incluster` there is no cluster to
delete — `runtime down` is a deliberate no-op — so `platform down` is the actual
teardown and must remove what it installed.
