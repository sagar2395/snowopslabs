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
| `add-agents.sh` | no | Grow the running cluster to `<total>` agent nodes, ready to run the lab's app images. labctl calls it when a scenario's `requirements.agents` exceeds the cluster's agents. Only k3d has one; without it such a scenario asks for `AGENTS=<n>` and `labctl reset`. |
| `remove-agents.sh` | no | Shrink the running cluster towards `<total>` agent nodes, removing only the agents `add-agents.sh` created. A node holding a local volume, or whose pods cannot be drained, stays. labctl calls it after `scenario down`, with the most agents any active scenario still needs and never fewer than `AGENTS`. |
| `move-ingress.sh` | no | Add free host ports to the running cluster's ingress and record them in `~/.snowops/clusters/<name>.env`. `labctl init` calls it when the lab's Grafana does not answer through the recorded port from this machine. Only k3d has one (`k3d cluster edit --port-add`); kind cannot add ports to a running cluster. |

Scripts every local profile shares live in `runtimes/_lib/`: `docker.sh` (sourced
helpers for the Docker engine and the cluster record) and `move-domain.sh`
(`<cluster> <old> <new>`: rewrite the lab's hostnames to a new suffix in place,
which `labctl init` runs for a lab on a legacy default suffix).

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
- `up.sh` **heals** an existing cluster instead of replacing it. After a Docker
  or colima restart k3d's node containers come back at once and agents can
  lose k3s; `up.sh` restarts the cluster in order (servers, then agents), waits
  for every node to stay Ready, and restarts a node that does not
  ([R04 §9](runbooks/R04-lab-lifecycle.md#9-the-lab-after-the-host-restarts)).
- `up.sh` **never deletes** an existing cluster. If it still cannot be reached
  it exits non-zero and points at `labctl reset`, which rebuilds on purpose.
- `up.sh` **records the ingress ports it bound** in
  `$SNOWOPS_HOME/clusters/<name>.env` (default `~/.snowops`). When 80/443 are
  busy it falls back to free ports (8080/8443 and up), and labctl reads this
  record so every URL, printed hint and check uses the real port. `down.sh`
  removes the record.
- kind reaches the ingress through **host ports, not a load balancer**. `up.sh`
  maps the host's 80/443 to the control-plane node and labels it
  `ingress-ready=true`; kind has no load balancer, so on `PROFILE=kind` Traefik
  binds `hostPort` 80/443 on that node
  ([R05](runbooks/R05-platform-components.md#helm-truths-this-repo-has-been-bitten-by)).
- The record also holds the lab's **domain suffix**, which it keeps across
  restarts. A lab still on a legacy default (`k3d.local`, `kind.local`) is moved
  to `<cluster>.localhost` by `labctl init`: `runtimes/_lib/move-domain.sh`
  rewrites the hosts of every Ingress, Traefik IngressRoute and cert-manager
  Certificate, then the record. A `DOMAIN_SUFFIX` set in the environment or
  `.env` is never moved.

### runtime.env keys

Every profile defines the same keys so downstream scripts can rely on them:

| Key | k3d | kind | incluster | Meaning |
|---|---|---|---|---|
| `INGRESS_CLASS` | `traefik` | `nginx` | `traefik` | Ingress controller the platform installs and routes through. |
| `STORAGE_CLASS` | `local-path` | `standard` | `standard` | Default `StorageClass` for PVCs. |
| `DOMAIN_SUFFIX` | `<cluster>.localhost` | `<cluster>.localhost` | `cluster.local` | Host suffix for ingress routes; content templates read `{{.DomainSuffix}}` for hostnames and `{{.IngressURLSuffix}}` (suffix plus a non-default port) for URLs. |
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
