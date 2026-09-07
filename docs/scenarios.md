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

**Category:** delivery

**What it deploys:**
- A Git server in the cluster (namespace `gitops`), serving a bare repo over
  `git://` on a PVC, seeded with the demo app's manifests
- ArgoCD — adopted from the `gitops/argocd` platform component, never a second
  copy of it
- One ArgoCD Application binding `demo/` in that repo to namespace
  `gitops-demo`, with automated sync, prune and self-heal
- ServiceMonitors for ArgoCD's three metrics endpoints, and a Grafana dashboard
  (`gitops-delivery`) plotting sync and health state

**Prerequisites:**
- Platform: ingress, gitops/argocd, monitoring/metrics, monitoring/grafana
- Apps: none

**The drill:** the learner clones the in-cluster repo over a port-forward,
changes the declared image tag and replica count, commits and pushes, and
watches ArgoCD reconcile the commit into the cluster. `verify` compares what
Git declares against what the cluster runs, and `learner-pushed-a-revision`
stays PENDING until a commit beyond the seed has actually been synced.

**Explore after activation:**
- Open the ArgoCD dashboard at `http://argocd.k3d.local` (admin / the password
  from `kubectl -n argocd get secret argocd-initial-admin-secret`)
- `kubectl -n gitops port-forward svc/git-server 9418:9418`, then
  `git clone git://127.0.0.1:9418/platform.git`
- Edit `demo/deployment.yaml`, push, and force a sync rather than waiting for
  the ~3 minute poll
- Prove self-heal: `kubectl -n gitops-demo scale deployment/gitops-demo
  --replicas=7` and watch ArgoCD put it back
- Prove prune: delete `demo/service.yaml` in Git, push, sync, and the Service
  leaves the cluster

> The Git daemon is unauthenticated, which is what makes the loop performable in
> a lab. It is the one thing here that must not be copied into a real cluster.

---

### Security & Compliance (`security-compliance`)

**Category:** security

**What it deploys:**
- Kyverno 3.9.0 (admission controller, via Helm)
- cert-manager v1.21.1 (via Helm) plus the lab CA chain: `selfsigned-issuer` →
  `lab-ca` → `lab-ca-issuer`
- 4 Kyverno ClusterPolicies, all shipped in **Audit** so activation disrupts
  nothing — promoting them is the drill:
  - `deny-privilege-escalation`
  - `require-non-root-user`
  - `require-resource-limits`
  - `disallow-latest-tag`
- Default-deny NetworkPolicies for the `go-api` namespace, plus the allowances a
  real workload needs: DNS, intra-namespace, mesh control plane, ingress and
  scraping
- `legacy-reporter` — a stand-in vendor workload violating three of the four
  policies, which the learner must exempt rather than fix
- Security Grafana dashboard (`/d/security-compliance`) — policy violations by
  mode and by policy, certificate expiry, admission latency

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana
- Apps: go-api

**What you do** (four graded tasks, shown as PENDING until you complete them):
1. Read the policy report, then remediate the **workload** so it satisfies every
   policy — `kubectl -n go-api get policyreports -o wide`
2. Promote the two Pod Security policies Audit → Enforce, and prove the webhook
   now rejects a root Pod with `kubectl apply --dry-run=server`
3. Triage the one you cannot fix: exempt `legacy-reporter` with a **label-scoped**
   `PolicyException`. The grading checks the scope — an exception matching the
   namespace instead of the label fails it
4. Issue the `go-api-tls` Certificate from `lab-ca-issuer`, wire it onto the
   Ingress, and prove the lab-signed leaf is what answers the TLS handshake

Order matters: promoting to Enforce before the workload is compliant rejects
go-api's own next rollout — which is exactly why Audit mode exists.

**Explore after activation:**
- `labctl scenario info security-compliance` — twelve numbered commands walk the
  whole drill
- Security dashboard at `http://grafana.k3d.local/d/security-compliance`
- Prove isolation: a pod in another namespace gets HTTP 000, while the ingress
  path still returns 200

**Traps this scenario documents:**
- A Kyverno `pattern` asserts on the path you wrote. `runAsNonRoot` set at
  **pod** level does not satisfy a pattern written against the **container**
  securityContext.
- Default-deny **egress** starves an injected mesh sidecar of istiod, so every
  new pod sits at 1/2 Running with the app container healthy — the
  `allow-mesh-control-plane` rule is what prevents it.
- Kyverno's CRD storage-version migration hook fails on upgrade
  (`Error: Unauthorized`), so it is disabled in the platform values; see
  [R05](runbooks/R05-platform-components.md).
- `features.policyExceptions` has two switches. `enabled: true` alone leaves
  `namespace: ''`, which restricts exceptions to a single unnamed namespace — the
  feature reads as on while every exception is silently ignored. The platform
  values set it to `'*'`.
- A `PolicyException`'s `ruleNames` must list the `autogen-` variants too, or the
  Pod is exempt while the Deployment that creates it is still rejected.

---

### Chaos Engineering (`chaos-engineering`)

**Category:** reliability

**What it deploys:**
- A `ServiceMonitor` for the Chaos Mesh controller-manager — the chart ships
  none, and its `prometheus.serviceMonitor` values key is silently ignored
- A `PodDisruptionBudget` for go-api (`minAvailable: 1`), deliberately against a
  single-replica Deployment
- A chaos Grafana dashboard: active/total experiments, PDB disruptions allowed,
  restarts, request rate, latency, CPU/memory, OOMKills
- Six Chaos Mesh experiments as **snippets**, applied one at a time by the
  learner: pod-kill, pod-failure, network-partition, network-delay, cpu-stress,
  memory-stress

Chaos Mesh itself is a **platform prerequisite** (`chaos/chaos-mesh`), not a
scenario component: its installer picks the containerd socket per `PROFILE` and
publishes the dashboard ingress, neither of which a scenario helm component can
do. Install it with `labctl platform up chaos/chaos-mesh`.

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana, chaos/chaos-mesh
- Apps: go-api

**The drill:** two checks start PENDING and only pass on the learner's work —
`chaos-experiment-was-run` (inject a failure) and `go-api-survives-pod-loss`
(scale go-api out until the PDB reports `disruptionsAllowed >= 1`). Measured on
a k3d lab: a pod-kill at one replica drops go-api from 20.3 to 4.2 rps for about
a minute; the same experiment at three replicas causes no dip at all.

**Explore after activation:**
- Chaos dashboard at `http://grafana.k3d.local/d/chaos-engineering`
- Chaos Mesh UI at `http://chaos.k3d.local`
- Load with `labctl traffic start --profile browse --rps 20` — never a curl loop
- Run one experiment: `kubectl apply -f
  scenarios/chaos-engineering/manifests/chaos-experiments.yaml -l experiment=pod-kill`

**Traps this scenario documents:**
- `minAvailable: 1` on a one-replica Deployment can never be satisfied, so the
  PDB reports `disruptionsAllowed 0` / `InsufficientPods` and blocks every node
  drain while doing nothing about involuntary failures. Scaling out is the fix.
- The experiment gauge is `chaos_controller_manager_chaos_experiments` with
  lowercase phases (`running`, `finished`), not `chaos_mesh_experiments`; the
  experiment's own namespace arrives as `exported_namespace` because the scrape
  target's `namespace` label wins. See
  [R13](runbooks/R13-observability-pipeline.md).
- go-api's `http_request_duration_seconds` is **handler** time, so it stays flat
  at ~4ms under a 300ms `NetworkChaos` delay. Network faults are only visible
  client-side (`curl -w time_starttransfer` through the ingress).
- A `network-partition` leaves the pod `1/1 Ready` with perfect pod metrics
  while every request through the ingress fails.
- Chaos Mesh's memory stressor takes stress-ng byte format (`256MB`), not a
  Kubernetes quantity — `256Mi` is rejected by the validating webhook.

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
- Two workloads consuming the same synced Secret `go-api-secrets`:
  `secret-consumer` mounts it as a **file** (`/etc/api/api-key`), `env-consumer`
  takes it as an **env var** — the control group
- A baseline value seeded at Vault `secret/go-api`
- A **Secret Rotation** Grafana dashboard (ESO sync calls, readiness, Vault API rate)

The Vault->ESO->Secret wiring itself belongs to the `secrets/external-secrets`
platform component; this scenario mounts what that component publishes and does
not re-create or remove it.

**Prerequisites:**
- Platform: secrets/vault, secrets/external-secrets, monitoring/metrics
- Apps: go-api

**Checks (9):** ESO controller ready, ExternalSecret ready, **ESO metrics scraped**
(promql — an empty dashboard fails `verify`), both consumers running, **rotation
performed** (pending — you rotated the value away from the baseline), **rotation
reached pod** (pending — the file in the running container equals Vault), **no
redeploy needed** (the consumer never restarted or rolled), **env consumer did not
see it** (pending — the env-var pod is still on the old value, which is the lesson).

**Run the drill:**
- Watch the pod read the file: `kubectl -n go-api logs -l app=secret-consumer -f`
- Rotate in Vault: `kubectl -n vault exec vault-0 -- sh -c 'VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=root vault kv put secret/go-api api-key=rotated-v2'`
- Watch the new value appear in the log with no restart (~90s), then compare:
  `kubectl -n go-api exec deploy/secret-consumer -- cat /etc/api/api-key` against
  `kubectl -n go-api exec deploy/env-consumer -- sh -c 'echo $API_KEY'`
- Grade it: `labctl scenario verify secrets-management`

> **Two hops, not one:** ESO refreshes the Secret every 15s, then the kubelet
> refreshes the mounted file on its own cycle — end to end is up to ~90s, so a
> PENDING `rotation-reached-pod` for a minute is normal.

> **Env vars are the trap:** the kubelet cannot rewrite a running process's
> environment, so `env-consumer` stays on its start-time value until something
> rolls it. Mount a secret as a file when you want rotation to be free. (The key is
> bound with `secretKeyRef`, not `envFrom`: `api-key` is not a valid env var name
> and `envFrom` would skip it silently.)

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
- The shape the drill needs: go-api scaled to 3 replicas. The
  `PodDisruptionBudget` is yours to write and apply — that is objective one
- A **Cluster Upgrade Drill** Grafana dashboard (`/d/cluster-upgrade-drill`)
  plotting nodes by kubelet version, go-api pods per node, cordoned nodes, the
  PDB's healthy-pods vs disruptions-allowed, and the success rate being graded

**Prerequisites:**
- Platform: ingress, monitoring/metrics
- Apps: go-api
- Runtime: k3d (multi-node)

**Checks (8):** PDB present, PDB protects >=2 pods, go-api >=2 ready, all nodes
Ready and uncordoned (script), no node left behind on the old version (script),
workload survived the roll and is still spread across nodes (script), traffic was
actually flowing (promql), **success rate >= 99%** across the upgrade window
(promql).

**Run the drill:**
- Start traffic: `labctl traffic start --profile steady --rps 20`
- Apply your PDB: `kubectl apply -f scenarios/cluster-upgrade-drill/manifests/baseline.yaml`
- Per worker node, cordon and drain it yourself:
  `kubectl cordon <node>` then
  `kubectl drain <node> --ignore-daemonsets --delete-emptydir-data --timeout=120s`
- Then replace the drained node:
  `TARGET_K3S_VERSION=<tag> bash scenarios/cluster-upgrade-drill/scripts/roll-node.sh <node>`
- Grade it: `labctl scenario verify cluster-upgrade-drill`

> **Honest scope:** k3d has no in-place node upgrade. `roll-node.sh` replaces one
> already-drained agent node with a fresh one on the target k3s image — a faithful
> rolling **worker** upgrade, and the same operation a managed node group performs.
> It refuses to touch a node you have not drained, refuses a downgrade, and refuses
> a kubelet more than three minor versions behind the API server. The control-plane
> node is left as-is; managed clusters upgrade it first.

> **Node replacement destroys local storage.** Any local-path PersistentVolume
> pinned to a node you replace is lost and its claim sits Pending forever — in
> this lab that routinely strands Prometheus itself, so the drill can no longer
> be graded. `roll-node.sh` warns before deleting such a node, and
> `scripts/reclaim-stranded-pvcs.sh` (try `DRY_RUN=true` first) releases the dead
> claims so their StatefulSets rebind. A drain also evicts the k6 traffic Job,
> which does not restart itself — check `labctl traffic status` after each roll.
> See [R04 §8](runbooks/R04-lab-lifecycle.md) for the full set of node-replacement
> traps.

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
