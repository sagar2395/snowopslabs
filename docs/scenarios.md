# Scenarios Guide

Scenarios are declarative playgrounds that install a collection of related tools, configurations, and dashboards to explore specific DevOps concepts.

## How Scenarios Work

Each scenario is a directory in `scenarios/` containing a `scenario.yaml` that declares:

- **Prerequisites** - platform components and apps that must be running
- **Components** - what to install (Helm charts, manifests, dashboards, scripts)
- **Explore hints** - URLs, commands, and tips for experimenting

The scenario engine handles installation order, template resolution, and state tracking.

## Using Scenarios

### List available scenarios

```bash
labctl scenario list
```

### Get details before activating

```bash
labctl scenario info observability-sre
```

This shows the full description, prerequisites, components that will be installed, and exploration hints.

### Activate a scenario

```bash
labctl scenario up observability-sre
```

The engine will:
1. Validate prerequisites (platform components and apps)
2. Install each component in order (Helm charts, kubectl manifests, Grafana dashboards)
3. Mark the scenario as active
4. Print exploration tips

### Deactivate a scenario

```bash
labctl scenario down observability-sre
```

Removes all components installed by the scenario.

### Check status

```bash
labctl scenario status
```

You can also use the web UI (`labctl ui`) to activate/deactivate scenarios with a single click.

---

## Available Scenarios

### Observability & SRE (`observability-sre`)

**Category:** observability

**What it deploys:**
- Loki (log aggregation, `grafana-community/loki`) — adopted when the platform already installed it
- Promtail (log shipping agent, `grafana/promtail`; deprecated upstream, see ADR-0012)
- Tempo (trace backend, `grafana-community/tempo`)
- Grafana Alloy (OTLP trace **collector** — the app exports here, Alloy forwards to Tempo)
- Alerting rules (PrometheusRule CRDs for high error rate, latency, pod restarts)
- SLO dashboards (Grafana JSON dashboards for availability, latency, error budget)

Load is generated with k6 (`labctl traffic start`), not a curl loop, so latency
changes show as queueing at a constant arrival rate. See runbook
[R13](runbooks/R13-observability-pipeline.md) for the end-to-end validation.

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana
- Apps: go-api

**Explore after activation:**
- Open Grafana at `http://grafana.k3d.local`
- Explore > Select Loki datasource > Query `{namespace="go-api"}`
- Explore > Select Tempo datasource > Search by service name
- Generate traffic: `for i in $(seq 1 100); do curl -s http://go-api.k3d.local/health; done`
- Trigger failures: `curl http://go-api.k3d.local/toggle-failure` then hit `/ready`
- Check alerts: `kubectl -n monitoring get prometheusrules`

---

### GitOps & CI/CD (`gitops-cicd`)

**Category:** gitops

**What it deploys:**
- ArgoCD (via Helm chart with Traefik ingress)
- ArgoCD Application CRDs pointing at `apps/go-api/deploy/helm/` and `apps/echo-server/deploy/helm/`
- Multi-environment setup (dev/staging namespaces with different values files)

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana
- Apps: go-api

**Explore after activation:**
- Open ArgoCD dashboard at `http://argocd.k3d.local`
- Login: admin / (password printed during install)
- Watch both apps synced in the ArgoCD UI
- Change a values file, observe ArgoCD detect drift and sync
- Perform a rollback via ArgoCD UI

---

### Security & Compliance (`security-compliance`)

**Category:** security

**What it deploys:**
- Kyverno (policy enforcement engine via Helm)
- cert-manager (TLS certificate management via Helm)
- 6 Kyverno ClusterPolicies:
  - `disallow-privileged-containers` (Enforce)
  - `require-labels` (Audit)
  - `disallow-root-user` (Audit)
  - `disallow-host-path` (Enforce)
  - `require-resource-limits` (Audit)
  - `disallow-latest-tag` (Audit)
- Network Policies (namespace isolation for go-api and echo-server)
- Security Grafana dashboard (policy violations, certificates, admission latency)

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana
- Apps: go-api

**Explore after activation:**
- Try deploying a non-compliant pod: `kubectl run nginx --image=nginx` (Kyverno blocks it)
- Check policy violations: `kubectl get policyreports -A`
- View security dashboard in Grafana
- Test network isolation between namespaces

---

### Chaos Engineering (`chaos-engineering`)

**Category:** chaos

**What it deploys:**
- Chaos Mesh (failure injection engine via Helm)
- PodDisruptionBudgets for go-api and echo-server
- 8 pre-built chaos experiments:
  - **PodChaos:** pod-kill (go-api), pod-kill (echo-server), pod-failure (go-api)
  - **NetworkChaos:** delay (echo-server to Redis, 500ms), partition (go-api to Traefik), packet loss (echo-server to Redis, 50%)
  - **StressChaos:** CPU stress (go-api), memory stress (echo-server)
- Chaos Grafana dashboard (experiment timeline, pod restarts, HTTP metrics, resource usage)

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana
- Apps: go-api

**Explore after activation:**
- Port-forward Chaos Dashboard: `kubectl -n chaos-mesh port-forward svc/chaos-dashboard 2333:2333`
- Run an experiment: `kubectl apply -f scenarios/chaos-engineering/manifests/chaos-experiments.yaml`
- Watch pods recover: `kubectl get pods -n go-api -w`
- Generate traffic during experiments: `while true; do curl -s http://go-api.k3d.local/health; sleep 0.1; done`
- Monitor impact in Grafana chaos dashboard
- Check PDB status: `kubectl get pdb -A`

---

### Autoscaling Under Load (`autoscaling-under-load`)

**Category:** scalability

**What it deploys:**
- A KEDA `ScaledObject` scaling go-api on Prometheus RPS (threshold ~25 RPS/replica, min 1 / max 6)
- A Grafana dashboard (replicas vs RPS, p99 latency)

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana, autoscaling/keda
- Apps: go-api

**Checks (5):** KEDA operator ready, ScaledObject present, KEDA HPA created,
go-api scaled up (≥3, post-spike), p99 latency within SLO.

**Explore after activation:**
- Drive the spike: `labctl traffic start --profile spike --rps 10`
- Watch scaling: `kubectl -n go-api get hpa keda-hpa-go-api -w`
- Verify post-spike: `labctl scenario verify autoscaling-under-load`

Full walkthrough: `docs/runbooks/10-stack-expansion.md` (task 057).

---

### Mesh Traffic Management (`mesh-traffic-management`)

**Category:** networking · **Mesh provider:** Istio (default)

**What it deploys:**
- Two go-api versions (v1, v2) behind one Service, split **90/10** by an Istio `VirtualService`
- A `DestinationRule` mapping the `version` label to subsets
- A **STRICT** `PeerAuthentication` (mTLS) on the canary workload
- Stage 2: a mesh-level **latency fault** (2s fixed delay on the v2 subset)

**Prerequisites:**
- Platform: ingress, mesh, monitoring/metrics
- Apps: go-api

**Checks (5):** istiod ready, go-api-v1 ready, go-api-v2 ready, VirtualService
present, STRICT PeerAuthentication present.

**Explore after activation:**
- Observe the split: send requests to `go-api-canary` and group by version
- Inspect the weighted route: `kubectl -n go-api get virtualservice go-api-canary -o jsonpath='{.spec.http[0].route}'`
- Confirm mTLS: `kubectl -n go-api get peerauthentication go-api-mtls -o jsonpath='{.spec.mtls.mode}'`

---

### Event-Driven Architecture (`event-driven-arch`)

**Category:** data

**What it deploys:**
- An `orders` KafkaTopic (3 partitions) on the Strimzi `lab-kafka` cluster
- A continuous producer and a consumer group (`order-processors`) using the Apache Kafka console tools
- Stage 2: ramps producers to 3× to build **consumer lag**

**Prerequisites:**
- Platform: data/kafka
- Apps: go-api

**Checks (4):** Kafka cluster ready, orders topic ready, producer running,
consumer running.

**Explore after activation:**
- Watch lag: `kubectl -n kafka exec -it lab-kafka-dual-role-0 -- bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group order-processors`
- Drain it: `kubectl -n kafka scale deployment/orders-consumer --replicas=3` (or add a KEDA Kafka-lag `ScaledObject`)

---

### Secrets Management & Rotation (`secrets-management`)

**Category:** security

**What it deploys:**
- A `SecretStore` + `ExternalSecret` syncing Vault `secret/go-api` → k8s Secret `go-api-secrets`
- Stage 1 seeds a baseline value; stage 2 **rotates** it in Vault

**Prerequisites:**
- Platform: secrets/vault, secrets/external-secrets
- Apps: go-api

**Checks (4):** Vault running, ESO controller ready, ExternalSecret ready,
**rotation propagated** (script check — the synced Secret equals the rotated value, no redeploy).

**Explore after activation:**
- Read the synced Secret: `kubectl -n go-api get secret go-api-secrets -o go-template='{{index .data "api-key" | base64decode}}'`
- Rotate again: `vault kv put secret/go-api api-key=my-new-value` (via the Vault pod) and watch it propagate

> **No secrets in git:** the Vault dev token comes from `VAULT_DEV_ROOT_TOKEN` (default `root`).

---

### Day-2 Drill: Node Drain Under Load (`node-drain-drill`)

**Category:** operations

**What it deploys:**
- A `PodDisruptionBudget` (`maxUnavailable: 1`) for go-api so a node drain cannot
  take all replicas down at once

**Prerequisites:**
- Platform: ingress, monitoring/metrics
- Apps: go-api
- A multi-node cluster (k3d defaults to 2 agents; `AGENTS=2 labctl runtime up`)

**Checks (5):** PDB present, PDB protects ≥2 pods, go-api ≥2 ready, **success rate
≥ 99.5%** through the drain (promql), no node left cordoned (script).

**Run the drill:**
- Start traffic: `labctl traffic start --profile steady --rps 20`
- Drain a node: `bash scenarios/node-drain-drill/scripts/drain.sh`
- Grade it: `labctl scenario verify node-drain-drill`

---

### Day-2 Drill: Rolling Cluster Upgrade Under Load (`cluster-upgrade-drill`)

**Category:** operations

**What it deploys:**
- A `PodDisruptionBudget` (`maxUnavailable: 1`) for go-api so the node roll keeps
  the app available

**Prerequisites:**
- Platform: ingress, monitoring/metrics
- Apps: go-api
- Runtime: k3d (multi-node)

**Checks (4):** PDB present, go-api ≥2 ready, all nodes Ready & schedulable
(script), **success rate ≥ 99%** across the upgrade window (promql).

**Run the drill:**
- Start traffic: `labctl traffic start --profile steady --rps 20`
- Roll workers to a newer version:
  `TARGET_K3S_VERSION=v1.29.4-k3s1 bash scenarios/cluster-upgrade-drill/scripts/upgrade.sh`
- Grade it: `labctl scenario verify cluster-upgrade-drill`

> **Honest scope:** k3d has no in-place node upgrade. `upgrade.sh` drains and
> replaces each agent node on the target k3s image — a faithful rolling **worker**
> upgrade. The control-plane node is left as-is; managed clusters upgrade it first.

---

### Day-2 Drill: Namespace Backup & Restore (`backup-restore-drill`)

**Category:** operations

**What it deploys:**
- A `restore-marker` ConfigMap in the go-api namespace whose presence and value
  prove the backup/restore round-trip
- A `data-writer` Deployment mounting a PVC (`restore-data`) that persists a
  random `boot-id` on its PersistentVolume — so PV-data loss is **observable**,
  not just described

**Prerequisites:**
- Apps: go-api
- `jq` installed (used to scrub server-managed fields from the manifest archive)

**Checks (7):** namespace exists, go-api running, data-writer running, restore
PVC present, restore marker present, marker value intact, **backup archive
exists** (script). The last four are marked `pending` — they render as PENDING
(not FAIL) until you complete the matching drill step.

**Run the drill (real `kubectl` — the commands you'd use on a live cluster):**
- Note the PV data: `bash scenarios/backup-restore-drill/scripts/observe-pv-data.sh go-api`
- Back up: `bash scenarios/backup-restore-drill/scripts/backup.sh go-api`
  (read the script — it is `kubectl get … -o json | jq <scrub>`)
- Simulate loss: `kubectl -n go-api delete configmap restore-marker`
- Restore: `kubectl apply --server-side --force-conflicts -f .labctl/backups/go-api-latest.json`
  (`restore.sh` wraps this and also recreates the namespace if it was deleted)
- Grade it: `labctl scenario verify backup-restore-drill`
- **Harder:** `kubectl delete namespace go-api`, then
  `bash scenarios/backup-restore-drill/scripts/restore.sh go-api`, then re-run
  `observe-pv-data.sh` — the `boot-id` changed.

> **Manifest-level backup:** archives round-trip Kubernetes objects, not
> PersistentVolume data. Delete just the ConfigMap and the `boot-id` survives a
> restore; delete the whole namespace and the PVC object comes back bound to a
> brand-new empty volume — the object round-trips, the data does not. For
> stateful data use a volume snapshot or Velero with restic.

---

### Cost & Capacity: Right-Sizing (`cost-right-sizing`)

**Category:** cost

**What it deploys:**
- go-api's requests over-provisioned in place to 40× CPU (2000m) and 32× memory
  (1Gi) via `kubectl set resources` — the deliberate "before" state you observe in
  OpenCost. (The inflate mutates the running Deployment directly rather than via a
  Helm upgrade, so it works no matter how go-api was deployed.)

**Prerequisites:**
- Platform: cost/opencost, monitoring/metrics, ingress
- Apps: go-api

> `cost/opencost` needs `monitoring/metrics` (Prometheus + kube-state-metrics) to
> compute allocation — without it the OpenCost API returns no cost data. `scenario
> up` warns if a platform prerequisite is missing and prints the `labctl platform
> up <component>` command to install it.

**Checks (5):** go-api running, **CPU request ≤ 100m** (script), **memory request
≤ 256Mi** (script), /health endpoint returns 200, OpenCost running.

**The exercise:**

1. `labctl scenario up cost-right-sizing` — inflates requests; checks **fail**
2. `labctl traffic start --profile steady` — drive load (or the UI **Traffic** tab)
   so "healthy under steady traffic" is exercised and usage shows up in OpenCost
3. Open the OpenCost UI at **http://opencost.k3d.local** (OpenCost is exposed via
   ingress). Run `sudo labctl hosts add` once to add `opencost.k3d.local` to
   `/etc/hosts`. No ingress DNS entry? Fall back to
   `kubectl -n opencost port-forward svc/opencost 9090 &` and open
   `http://localhost:9090`.
4. Observe inflated cost in OpenCost (go-api namespace)
5. Right-size: `kubectl -n go-api set resources deployment go-api --requests=cpu=50m,memory=32Mi`
6. `labctl scenario verify cost-right-sizing` — all checks **pass**

> **k3d note:** no real billing API — OpenCost uses on-prem pricing defaults
> (~$0.048/CPU-hr). Cost numbers are relative; the before/after contrast is real.

---

## Scenario YAML Format

```yaml
apiVersion: scenario.snowops.net/v2  # schema version (optional; defaults to v2)
name: my-scenario                    # Must match directory name
displayName: "My Scenario"           # Shown in UI and CLI
description: "What this scenario teaches"
category: observability              # Grouping label

prerequisites:
  platform:                          # Required platform components
    - ingress
    - monitoring/metrics
  apps:                              # Required apps
    - go-api

runtimes:                            # Compatible runtimes (optional)
  - k3d
  - kind

components:                          # What to install (in order)
  - name: my-chart
    type: helm                       # helm | manifest | grafana-dashboard | script
    chart: repo/chart-name
    repo: https://charts.example.com # Helm repo URL
    version: "1.0.0"
    namespace: my-ns
    valuesFile: values/my-chart.yaml

  - name: my-manifests
    type: manifest
    path: manifests/my-resources.yaml
    namespace: my-ns

  - name: my-dashboards
    type: grafana-dashboard
    path: dashboards/
    namespace: monitoring

  - name: my-setup
    type: script
    script: scripts/setup.sh

explore:
  urls:
    - label: "My Dashboard"
      url: "http://my-app.{{.DomainSuffix}}"

  commands:
    - label: "Check status"
      command: "kubectl get pods -n my-ns"

  tips:
    - "First, generate some traffic to see data in dashboards"
    - "Try breaking things to see alerts fire"
```

### Verified (curation metadata)

Content carries an optional `verified` flag:

```yaml
verified: true    # confirmed end-to-end on a fresh cluster
```

This is **curation metadata, not a user-facing badge** — it records which
scenarios/incidents have been confirmed to activate, pass their checks, and tear
down cleanly on a fresh cluster, so the nightly e2e job knows what to guard and
maintainers know what's still to confirm. It is intentionally not surfaced in the
CLI or UI: shipped content is expected to work, so it isn't labelled for users.
Absent or `false` simply means "not yet confirmed end-to-end."

### References and snippets

Two optional blocks turn a scenario into a jumping-off point for hands-on
learning. Both are shown by `labctl scenario info <name>` and are template-
resolved, so they display with the deployment's real namespaces and domains.

```yaml
references:                            # links to the upstream tool/docs
  - label: "KEDA — ScaledObject specification"
    url: "https://keda.sh/docs/latest/reference/scaledobject-spec/"
    note: "Optional one-liner on why this link is relevant."

snippets:                              # applyable manifest fragments
  - label: "KEDA ScaledObject for go-api"
    description: "Optional context shown above the manifest."
    path: manifests/scaledobject.yaml  # a file in the scenario dir, OR:
  - label: "A quick inline manifest"
    yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: demo
        namespace: "{{.MonitoringNamespace}}"
  - label: "Helm values (not a kubectl manifest)"
    path: values/overprovisioned.yaml
    apply: "helm upgrade -f -  (or translate to 'kubectl set resources')"
```

- A `reference` needs a `label` and an `http(s)` `url`; `note` is optional.
- A `snippet` needs a `label` and **exactly one** of `yaml` (inline manifest
  text) or `path` (a file relative to the scenario directory — reuse the
  existing `manifests/` convention rather than duplicating). `labctl validate`
  fails if a `path` does not resolve, naming the file and the snippet.
- Snippet bodies are template-resolved, so a `path` snippet can reuse the same
  manifest the engine applies, and an inline `yaml` snippet can reference
  `{{.MonitoringNamespace}}` etc. — it prints ready to `kubectl apply -f -`.
- `apply` (optional) overrides the per-snippet "apply with" hint. It defaults to
  `kubectl apply -f -`; set it when the snippet is **not** a kubectl manifest
  (e.g. a Helm values file) so the learner isn't told to `kubectl apply` something
  that isn't appliable.

### Template Variables

URLs and commands support Go template variables:

| Variable | Example Value | Description |
|----------|--------------|-------------|
| `{{.DomainSuffix}}` | `k3d.local` | Domain suffix from active runtime |
| `{{.ProjectRoot}}` | `/path/to/project` | Absolute path to project root |

### Component Types

| Type | What It Does |
|------|-------------|
| `helm` | Adds Helm repo, installs chart with values file |
| `manifest` | Applies Kubernetes YAML via `kubectl apply` |
| `grafana-dashboard` | Creates ConfigMap from dashboard JSON files (picked up by Grafana sidecar) |
| `script` | Runs a shell script (`script:` path relative to the scenario directory) |

---

## Scenario Format v2 — stages, objectives, checks

Format v2 turns a scenario from "install these things" into a **verifiable
simulation**. Three optional blocks extend the format above (v1 scenarios
keep working unchanged):

```yaml
objectives:                          # human-readable goals
  - "Aggregate application logs in Loki"
  - "Keep p99 latency under 300ms"

stages:                              # ordered groups of components
  - name: baseline                   # replaces the flat components: list
    description: Install the baseline stack
    components:                      # same component schema as v1
      - name: loki
        type: helm
        chart: grafana-community/loki
        version: "18.12.0"           # always pinned — see config/versions.env
        platformValues: logging/loki # base values; never a second copy
        valuesFile: values/loki.yaml # optional overlay, applied on top
        adopt: true                  # reuse an existing release, do not upgrade it
        ...
  - name: inject-failure
    components: [...]

checks:                              # machine-verifiable assertions
  - name: loki-ready
    type: kubectl                    # http | kubectl | promql | script
    resource: statefulset/loki      # kubectl: type/name or a bare type (existence)
    namespace: "{{.MonitoringNamespace}}"
    jsonpath: "{.status.readyReplicas}"
    operator: ">="                   # == != < <= > >= contains
    value: "1"

  - name: grafana-reachable
    type: http
    url: "http://grafana.{{.DomainSuffix}}"
    expectStatus: 200                # default 200; bodyContains: optional

  - name: latency-ok
    type: promql                     # queries $PROMETHEUS_URL (default
    query: 'histogram_quantile(...)' # http://prometheus.<DOMAIN_SUFFIX>)
    operator: "<"
    value: "0.3"

  - name: custom
    type: script                     # exit 0 = pass; runs with DOMAIN_SUFFIX,
    script: checks/custom.sh         # MONITORING_NAMESPACE, PROJECT_ROOT set
    timeoutSeconds: 60               # any check may override the 30s default
    remediation: "run the fix: …"    # optional: shown under a failing check
    pending: true                    # optional: render PENDING (not FAIL) until
                                     # the user performs the matching drill step
```

**`remediation` / `pending`** make `verify` teach instead of alarm. A failing
check with a `remediation` prints that fix under a **Next step(s)** heading; the
generic "a pod may still be starting / --watch" hint is shown only for a genuine
failure that has no remediation. A failing `pending` check renders **PENDING**
and is reported as an incomplete drill step, so "you haven't run the backup yet"
never reads as "the scenario is broken".

Rules (enforced at load time — an invalid scenario refuses to load and CI
fails on it):

- Use **either** `components` (v1) **or** `stages` (v2), never both.
- Stage names, component names, and check names must be unique and non-empty.
- Each check type accepts only its own fields (an `http` check with a
  `query` is rejected, not ignored).
- Checks run in declaration order; a failing check does not stop the rest.

Run the checks with:

```bash
labctl scenario verify my-scenario            # one shot, exit 1 on failure
labctl scenario verify my-scenario --watch    # poll until green or timeout
```

The same checks are the grading primitive for the upcoming incident engine
and challenge mode (see `docs/PRODUCT.md`) — write them as "what must be
true when this scenario is healthy".

## Sharing scenarios — external content roots

Scenarios do not have to live in this repo. Because a scenario is just a
directory, sharing one is git and nothing else — there is no pack format,
registry or publish step (see
[ADR-0008](adr/0008-content-extensibility-seam.md)).

Keep your scenarios in your own repository, laid out one directory per
scenario, then point SnowOps Labs at it:

```bash
git clone https://github.com/org/our-scenarios ~/our-scenarios
export SNOWOPS_CONTENT_PATH=~/our-scenarios
labctl scenario list        # your scenarios appear, badged as external
```

`SNOWOPS_CONTENT_PATH` accepts several roots separated by the OS path
separator. How it behaves:

- Each root is scanned for directories containing a `scenario.yaml`, and every
  one is schema-validated. An invalid scenario is reported by name and skipped;
  it never hides the rest.
- External scenarios work with `up`, `down`, `verify` and `info` exactly like
  in-repo ones, and show their root in the SOURCE column of `scenario list`.
- **In-repo scenarios win name collisions.** An external scenario with a
  colliding name is skipped with the conflict named.
- Asset paths must stay inside the scenario directory — absolute paths and `..`
  traversal are rejected at validation.

**Security:** a scenario's components run scripts and apply manifests on your
cluster with your credentials. Only point `SNOWOPS_CONTENT_PATH` at sources
you trust, and read them first. This is the same trust level as running any
script from that repository.

## Creating a New Scenario

1. Create directory: `scenarios/my-scenario/`
2. Write `scenario.yaml` following the format above (prefer v2 with checks)
3. Add supporting files under `values/`, `manifests/`, `dashboards/` as needed
4. Test: `labctl scenario up my-scenario`, then `labctl scenario verify my-scenario`

The scenario engine auto-discovers any directory under `scenarios/` that
contains a valid `scenario.yaml`. Schema validation runs in CI for every
scenario in the repo (`internal/scenario/repo_test.go`), so a
malformed scenario cannot merge to main.
