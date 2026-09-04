# Scenario catalog

Scenarios are declarative labs. Each installs a set of related tools,
configurations and dashboards so you can explore one platform-engineering
concept hands-on, then grades your work with machine-verifiable checks.

- **Writing one?** The full YAML reference is the
  [scenario schema](reference/scenario-schema.md), and the guided walkthrough is
  [your first scenario](authoring/first-scenario.md).
- **Running one?** The commands are in the
  [CLI reference](reference/cli/scenarios.md).

This page is the catalog of what ships in the repository.

## How they work

A scenario is a directory under `scenarios/` containing a `scenario.yaml` that
declares its prerequisites (platform components and apps that must be running),
its components (Helm charts, manifests, dashboards, scripts), its checks, and
explore hints. The engine handles installation order, template resolution and
state tracking.

```bash
labctl scenario list                       # what is available, and what is active
labctl scenario info observability-sre     # description, prerequisites, components, hints
labctl scenario up observability-sre       # activate
labctl scenario verify observability-sre   # grade
labctl scenario down observability-sre     # deactivate
```

`scenario up` validates prerequisites, installs each component in order, marks
the scenario active and prints the explore tips. `scenario down` removes what it
installed. The web UI (`labctl ui`) does the same in one click.

---

## Available scenarios

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

Full YAML reference: [scenario schema](reference/scenario-schema.md).
Sharing scenarios from your own repository, and the security implications of
doing so, are covered there too.
