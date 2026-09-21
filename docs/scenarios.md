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

**What it stages:**
- Loki (log aggregation, `grafana-community/loki`) — adopted when the platform already installed it
- Promtail (log shipping agent, `grafana/promtail`; deprecated upstream, see ADR-0012)
- Tempo (trace backend, `grafana-community/tempo`)
- Grafana Alloy (OTLP trace **collector** — the app exports here, Alloy forwards to Tempo)
- Alerting rules (PrometheusRule CRDs: error rate, latency, memory saturation, CPU throttling, readiness)
- Three dashboards: Golden Signals (with a Tempo traces panel), an SLO dashboard, and a Log Explorer

**What it deliberately does not do:** wire the application to the collector.
That is objective 1, and a stage that performs it leaves a check asserting what
the stage just set. The `reset-tracing-wiring` component runs
`disable-tracing.sh` on activation *and* on teardown, so every run starts
unwired and ends clean.

**The drill:**
1. Wire the app to Alloy — `kubectl set env deployment/<app> OTEL_EXPORTER_OTLP_ENDPOINT=...`
   (or run `scripts/enable-tracing.sh`). The app never learns which trace backend
   is behind the collector.
2. Drive real load with k6: `labctl traffic start --profile steady --rps 25`.
3. Follow one request across all three signals — Loki gives a `trace_id`, Grafana
   turns it into a jump to that trace in Tempo.
4. Break readiness on purpose and take `PodNotReady` from inactive to firing.

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana
- Apps: the bound workload, which must declare `prometheus-metrics`,
  `otlp-tracing` and `readiness-toggle`

**Checks (11):** six guard the stack labctl stands up (Loki, Promtail, Tempo,
Alloy, the PrometheusRule's `release` label, Grafana). Five grade the drill and
are `pending` until it is done: the app is under real load, it exports to the
collector, spans arrived in Tempo, its logs are queryable in Loki and carry
trace ids, and an alert from this scenario actually fired.

The pairing matters. `app-exports-traces-to-collector` reads the environment
variable and gives the exact command to fix it; `traces-arrived-in-tempo` reads
Tempo. An endpoint can be set on a Deployment that never sends a span — wrong
port, collector down, no traffic — and all of those pass the setting check.

**Traps this scenario encodes:**
- **History satisfies an unwindowed query.** Tempo and Loki retain what a
  previous run produced, so a check that asks "are there spans?" is green the
  moment the lab has ever done this exercise. Both script checks bound their
  queries to a recent window (`LOOKBACK_SECONDS`, default 900). Tempo filters at
  block granularity, so this rules out a run from yesterday rather than one from
  ten minutes ago.
- **Two of the six alert rules could never fire.** They referenced
  `container_spec_memory_limit_bytes` and
  `container_cpu_cfs_throttled_seconds_total`, neither of which exists on k3s or
  kind. The limit now comes from `kube_pod_container_resource_limits`
  (kube-state-metrics) and throttling from the ratio of
  `container_cpu_cfs_throttled_periods_total` to `container_cpu_cfs_periods_total`.
  A rule with a dead expression reports `health: ok` and stays `inactive` forever.
- **Tempo's HTTP API listens on 3200, not 3100.** A Grafana datasource on the
  wrong port fails every query and looks exactly like "no traces were recorded".
  The check scripts query *through Grafana's datasource proxy* for this reason:
  probing Tempo directly would pass with a broken datasource, which is the path
  the learner actually clicks.

Load is generated with k6 (`labctl traffic start`), not a curl loop, so latency
changes show as queueing at a constant arrival rate. See runbook
[R13](runbooks/R13-observability-pipeline.md) for the end-to-end validation.

**Explore after activation:**
- Golden Signals dashboard: `http://grafana.k3d.local/d/observability-sre`
- Explore > Loki > `{namespace="<workload-ns>"} | json` — expand a line, click its `trace_id`
- Explore > Tempo > search by `service.name`
- Check alerts: Prometheus > Alerts, or `kubectl -n monitoring get prometheusrules`

---

### GitOps & CI/CD (`gitops-cicd`)

**Category:** delivery

**What it deploys:**
- A Git server in the cluster (namespace `gitops`), serving a bare repo over
  `git://` on a PVC, seeded with two paths: `demo/` (a working release) and
  `broken/` (a release that cannot become healthy)
- ArgoCD — adopted from the `gitops/argocd` platform component, never a second
  copy of it
- Two ArgoCD Applications, each watching one of those paths, both with automated
  sync, prune and self-heal
- ServiceMonitors for ArgoCD's three metrics endpoints, and a Grafana dashboard
  (`gitops-delivery`) plotting sync and health state

**Prerequisites:**
- Platform: ingress, gitops/argocd, monitoring/metrics, monitoring/grafana
- Apps: none

**Checks (10).** Six are always enforced; four are the drill, and each one grades
an objective that used to be described and never tested:

| Pending check | What it proves |
|---|---|
| `learner-pushed-a-revision` | `demo/deployment.yaml` at HEAD declares something other than the seed commit did |
| `self-heal-observed` | ArgoCD scaled the Deployment back **from** a replica count Git has never declared |
| `prune-removed-a-resource` | `demo/service.yaml` is absent from Git **and** the Service is gone from the cluster |
| `broken-app-repaired` | the `gitops-broken` Application is Synced **and** Healthy |

**The drill:** clone the in-cluster repo over a port-forward, change the declared
image tag and replica count, push, and watch ArgoCD reconcile it. Then prove the
three things that separate GitOps from `kubectl`: scale the Deployment by hand
and be reverted, delete a manifest from Git and watch the resource leave, and
repair the deliberately-broken release — in Git, because `kubectl set image` is
reverted within seconds.

**Explore after activation:**
- Open the ArgoCD dashboard at `http://argocd.k3d.local` (admin / the password
  from `kubectl -n argocd get secret argocd-initial-admin-secret`)
- `kubectl -n gitops port-forward svc/git-server 9418:9418`, then
  `git clone git://127.0.0.1:9418/platform.git`
- Edit `demo/deployment.yaml`, push, and force a sync rather than waiting for
  the ~3 minute poll

> **`Synced` does not mean `working`.** `gitops-broken` syncs perfectly — its
> manifests are valid YAML — and is still `Degraded`, because the image tag in
> them does not exist. Sync status is a statement about your pipeline; health
> status is a statement about your release. Telling them apart is objective 7.

> **Self-heal is graded from the event trail, not from catching it live.** The
> revert lands in a couple of seconds, ArgoCD appends no `.status.history` entry
> for it, and `argocd_app_sync_total` resets when the controller restarts. What
> survives is a `ScalingReplicaSet` event scaling *from* a count Git never
> declared *to* the count it does — which a rolling update cannot produce, since
> its own steps only move between declared counts.

> **Do not compare `.status.sync.revision` with repo HEAD.** It is the revision
> ArgoCD last *compared at*, so it lags behind HEAD after a commit to a path the
> Application does not watch, and jumps past the last commit that touched its own
> path after the next sync. Both comparisons produce a check that is red for
> reasons the learner did not cause.

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
- Default-deny NetworkPolicies for the the bound workload namespace, plus the allowances a
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
   policy — `kubectl -n <workload-ns> get policyreports -o wide`
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


> **What NetworkPolicy actually enforces on k3d — measured, 2026-09-09.**
> Default-deny is real for ordinary pod-to-pod traffic: with `default-deny-all`
> in place a pod in `default` gets `curl` exit code `000`, and with no policies
> at all the same probe gets `200`. But **traffic from the ingress controller and
> from Prometheus reaches the workload whether or not a rule admits them** — both
> kept working with only `default-deny-all` applied. In testing, adding a
> well-formed `namespaceSelector` ingress allow did not re-open a blocked
> pod-to-pod flow either. So `allow-ingress-traffic` and `allow-monitoring-scrape`
> are correct Kubernetes and belong in the manifest, but their effect is not
> demonstrable on this runtime; the deny is. The root cause has not been isolated
> — likely source-NAT or a k3s NetworkPolicy controller limitation — and is
> tracked as open.

> **`namespace-isolation-enforced` now proves enforcement, not just correctness.**
> Reading the policy set shows the rules are *written* right and says nothing
> about whether the CNI applies them — and a cluster that ignores NetworkPolicy
> entirely passes every YAML assertion while the scenario teaches nothing. The
> check ends by opening a connection from a pod outside the namespace and
> requiring it to be refused. Verified both ways: `200` with the policies
> removed, `000` with them in place.

### Chaos Engineering (`chaos-engineering`)

**Category:** reliability

**What it deploys:**
- A `ServiceMonitor` for the Chaos Mesh controller-manager — the chart ships
  none, and its `prometheus.serviceMonitor` values key is silently ignored
- A `PodDisruptionBudget` for the workload (`minAvailable: 1`), deliberately
  against a single-replica Deployment
- A staging script that puts the shared workload **back to one replica** and
  clears any chaos objects an earlier run left behind, so the drill's premise
  actually holds; teardown restores the replica count it found
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

**The drill:** three checks start PENDING and only pass on the learner's work —
`chaos-experiment-was-run` (inject a failure), `workload-survives-pod-loss`
(scale out until the PDB reports `disruptionsAllowed >= 1`), and
`blast-radius-contained` (re-run the same experiment against the hardened
service and show the outage is gone). Measured on a k3d lab at 20 rps: a
pod-kill at one replica takes the request rate to **zero** for about ten
seconds; at three replicas the rate never leaves the floor.

**Explore after activation:**
- Chaos dashboard at `http://grafana.k3d.local/d/chaos-engineering`
- Chaos Mesh UI at `http://chaos.k3d.local`
- Load with `labctl traffic start --app <app> --profile browse --rps 20` — never a curl loop
- Run one experiment: `bash scenarios/chaos-engineering/scripts/inject.sh pod-kill --app <app> --namespace <namespace>`
  (with no argument it lists what is available)

**Traps this scenario documents:**
- `minAvailable: 1` on a one-replica Deployment can never be satisfied, so the
  PDB reports `disruptionsAllowed 0` / `InsufficientPods` and blocks every node
  drain while doing nothing about involuntary failures. Scaling out is the fix.
- The experiment gauge is `chaos_controller_manager_chaos_experiments` with
  lowercase phases (`running`, `finished`), not `chaos_mesh_experiments`; the
  experiment's own namespace arrives as `exported_namespace` because the scrape
  target's `namespace` label wins. See
  [R13](runbooks/R13-observability-pipeline.md).
- go-api's `http_server_request_duration_seconds` is **handler** time, so it stays flat
  at ~4ms under a 300ms `NetworkChaos` delay. Network faults are only visible
  client-side (`curl -w time_starttransfer` through the ingress).
- A `network-partition` leaves the pod `1/1 Ready` with perfect pod metrics
  while every request through the ingress fails.
- Chaos Mesh's memory stressor takes stress-ng byte format (`256MB`), not a
  Kubernetes quantity — `256Mi` is rejected by the validating webhook.
- **`chaos-experiments.yaml` is the one manifest the scenario never installs**,
  on purpose: applying all six at once makes the blast radius unattributable. But
  the engine only renders templates in files it installs, so nothing resolves its
  `{{.WorkloadName}}` / `{{.WorkloadNamespace}}` — `kubectl apply -f` on it fails
  with `namespaces "{{.WorkloadNamespace}}" not found`. `scripts/inject.sh`
  renders the binding and applies exactly one experiment by label.
- **A pod-kill outage is invisible at a one-minute resolution.** The service is
  gone for roughly ten seconds; sampled with `[15m:1m]` the floor never reaches
  zero and a "did it stay up" check passes on a single replica. Use a 30s
  sub-interval.
- **Measuring blast radius must start at the hardening**, not a fixed window
  back. The flatline on one replica *was* the first experiment working; counting
  it in the second measurement fails the learner for doing the drill correctly.

> **The client's view is now on the dashboard.** Every original panel read the
> app's own metrics, which cannot record a request the app never received — so
> the two failure modes the scenario calls out as invisible really were. Two k6
> panels (`k6_http_req_failed_rate`, `k6_http_req_duration_p99`) show failures
> and latency as a caller experiences them, next to the app's own numbers.

---

### Autoscaling Under Load (`autoscaling-under-load`)

**Category:** scalability

**What it deploys:**
- A Grafana dashboard (replicas vs RPS, p99 vs p50, tail amplification)

**What you do:** apply the KEDA `ScaledObject` yourself from the snippet in
`labctl scenario info` — declaring metric-driven autoscaling is the skill the
scenario exists to teach — then drive the spike and watch it react. Until you
do, `verify` reports those steps as PENDING rather than failed.

**Prerequisites:**
- Platform: ingress, monitoring/metrics, monitoring/grafana, autoscaling/keda
- Workload capabilities: `prometheus-metrics`

**Checks (6):** KEDA operator ready, ScaledObject present, KEDA HPA created,
workload scaled above its floor, latency did not degrade (p99/p50 under the
`MaxTailAmplification` knob), no errors under load.

The scale check reads the **autoscaler's** replica count, not the Deployment's,
so a hand-run `kubectl scale` does not satisfy it.

None of the checks encodes how fast one runtime is, so the scenario runs
unchanged against another application — `--app <name>`.

**Explore after activation:**
- Drive the spike: `labctl traffic start --profile spike --rps 10`
- Watch scaling: `kubectl -n <workload-ns> get hpa keda-hpa-<workload> -w`
- Verify post-spike: `labctl scenario verify autoscaling-under-load`


---

### Multi-Env Promotion (`env-promotion`)

**Category:** delivery

**What it deploys:**
- Three namespaces — `env-dev`, `env-staging`, `env-prod` — each running the same
  workload pinned to an explicit image tag, plus an `env-metadata` ConfigMap
  recording the tag each environment declares

**What you do:** build a new immutable tag, deploy it to dev only, then promote
it forward with `kubectl set image` and `kubectl rollout status`, and roll a bad
promotion back with `kubectl rollout undo`. There is deliberately no
`labctl env promote` — promotion *is* those kubectl verbs, and labctl's job is to
build the three environments and grade the result
([R06](runbooks/R06-multi-env-promotion.md)).

- A Grafana dashboard (`/d/env-promotion`) showing which tag each environment
  asked for, whether its rollout converged, and the ReplicaSet count that is
  `kubectl rollout history` as a graph

**Prerequisites:**
- Platform: ingress, monitoring/metrics
- Workload capabilities: none beyond the base contract

**Checks (9):** three namespaces exist, the workload runs in each, a newer
release reached prod, every environment's running image matches its declared tag
— compared on the full image reference, not just the tag — and **a rollback was
exercised**.

The rollback check grades the one trace a rollback leaves that rolling forward
cannot: prod serving a ReplicaSet *older* than the newest it has created. Rolling
forward always creates a new ReplicaSet; coming back re-activates an existing one
and bumps its revision. It does not care whether you used `rollout undo` or set
the image back by hand, because those are the same operation.

**Explore after activation:**
- Compare all three: `for e in dev staging prod; do curl -s <workload>-$e.<domain>/version; done`
- Promote: `kubectl -n env-staging set image deploy/<workload> <workload>=<workload>:v1.1.0`
- Then the recovery drill: ship prod a tag nobody built
  (`<workload>:v9.9.9`), verify — spec says v9.9.9, the rollout never converges,
  the pods still serve the old tag, and `verify` reports all three — then
  `kubectl -n env-prod rollout undo deploy/<workload>`

> **Read `image_spec`, not `image`:** the lab builds each version as both
> `vX.Y.Z` and `:latest`, so those tags share a digest and the kubelet reports
> whichever it resolved — commonly `docker.io/library/<workload>:latest` for a pod
> that correctly requested `<workload>:v1.1.0`. The consistency check compares
> pod `.spec` for the same reason.

> **Teardown removes the versioned images it built** and deliberately leaves
> `:latest` alone — that is the tag every other scenario's workload runs on.
> Copies already imported into the cluster's image store stay there until the
> node is replaced; the teardown says so rather than implying a clean slate.

---

### Mesh Traffic Management (`mesh-traffic-management`)

**Category:** networking · **Mesh provider:** Istio (default)

**What labctl stages:**
- The namespace enrolled into the mesh (`istio-injection=enabled`) and its
  deployments restarted, so pods actually get a sidecar
- Two versions of the workload (v1, v2) behind one Service, distinguished by
  `APP_VERSION` so `/version` reports which subset served a request
- A `DestinationRule` mapping the `version` label to subsets
- A **meshed** k6 load client, so the mesh has traffic to shape and report
- A Grafana dashboard (`mesh-traffic`): split by version, v2 share, latency
  percentiles, mTLS coverage

**What you do:** all three objectives. Nothing installs the routing for you —
apply the weighted `VirtualService`, then the header-matched latency fault, then
the STRICT `PeerAuthentication`. Until you apply the first one, the Service
round-robins both subsets at roughly 50% and the split check fails.

**Prerequisites:**
- Platform: ingress, mesh, monitoring/metrics
- Apps: the bound workload

**Checks (6):** istiod ready · every canary pod carries an `istio-proxy` sidecar
· v2's share stays above a 2% floor · and below 30% (versus ~50% for unrouted
round-robin) · v2 p99 latency shows the injected delay · an unmeshed plaintext
client is refused.

The two share checks are the bounds of one band, so each names a distinct
failure: rolled back to weight 0, and not routed at all.

The last four are `pending` — they are the drill, not the setup. The sidecar
check is a guard: without it, a namespace that was never enrolled grades as a
complete success while every routing rule sits inert.

**Explore after activation:**
- Grafana → the `mesh-traffic` dashboard: v2 share should fall from ~50% to ~10%
  the moment your `VirtualService` lands
- Promote the canary by shifting the weights (90/10 → 50/50 → 0/100) and
  re-applying
- Confirm mTLS: `kubectl -n <workload-ns> get peerauthentication <app>-mtls -o jsonpath='{.spec.mtls.mode}'`

> **Why the fault is header-matched.** An Istio fault applies to a whole
> `HTTPRoute`, so inside one weighted route there is no way to delay v2 and not
> v1. Matching `x-canary: always` first gives the canary traffic a route of its
> own — which is also how you would rehearse a slow release against opted-in
> testers. Istio takes the first match in `spec.http`, so that route must sit
> above the weighted one.

---

### Event-Driven Architecture (`event-driven-arch`)

**Category:** data

**The exercise:** a producer/consumer flow through Kafka is already running, and
producers have been ramped to five times what the consumer group can process, so
orders are piling up. Diagnose the backlog, then restore throughput by scaling
the group to the topic's partition count — and no further.

**What it stages:**
- An `orders` KafkaTopic (3 partitions) on the Strimzi `lab-kafka` cluster
- A producer and a consumer group (`order-processors`) built from the Apache
  Kafka console tools. The consumer sleeps 50ms per message, standing in for the
  work a real order processor does; that cost is what bounds one consumer to
  ~18 orders/second
- Stage 2: three producers at 5× rate (~29 orders/second), so lag climbs at
  roughly 11/s and a single consumer can never recover
- PodMonitors for both Kafka metric sources, and two Grafana dashboards

**What the learner does:** read the per-partition backlog from
`kafka-consumer-groups.sh` and the lag dashboard, then
`kubectl scale deployment/orders-consumer --replicas=3` and watch it drain. The
producers stay ramped throughout — stopping them is graded as not a fix.

**Prerequisites:**
- Platform: data/kafka, monitoring/metrics, monitoring/grafana
- Apps: none. This scenario runs entirely against Kafka and does not touch the
  bound workload.

**Checks (9):** Kafka cluster ready, orders topic ready, producers still ramped,
broker metrics scraped, consumer-lag metrics scraped, consumer group at full
width (three members, sustained), consumers within the partition ceiling,
backlog was observed, backlog drained.

Three of those carry the lesson. `consumer-group-at-full-width` reads Kafka's own
`kafka_consumergroup_members` rather than pod count, because a consumer pod can
be Ready and still not be in the group; it uses `min_over_time` so the members a
previous run leaves behind during termination cannot satisfy it.
`consumers-within-partition-ceiling` fails a learner who scales past three — a
partition is served by exactly one consumer in a group, so the extras sit idle
and add rebalance churn. And `backlog-was-observed` gates `backlog-drained`,
which is otherwise green at t=0 when nothing has piled up yet.

**Traps this scenario encodes:**
- A consumer that only prints its input absorbs any load the lab can generate,
  and the lag exercise becomes unperformable. The per-message cost is load-bearing.
- A consumer group outlives both the Deployments that joined it and the topic it
  read. Left behind, the next activation reports *negative* lag for several
  scrapes — committed offsets from the old topic ahead of the new one's log end.
  `scripts/reset-consumer-group.sh` deletes the group on activation and teardown,
  and the drained check clamps at zero as a second line of defence.
- Over-scaling puts the group into continuous rebalance, and
  `kafka-consumer-groups.sh --describe` then prints an empty table. That reads as
  "the group vanished"; it has not.

**Explore after activation:**
- Diagnose: `kubectl -n kafka exec lab-kafka-dual-role-0 -- bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group order-processors`
- Drain it: `kubectl -n kafka scale deployment/orders-consumer --replicas=3` (or add a KEDA Kafka-lag `ScaledObject` with `maxReplicaCount: 3`)

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

**Checks (9):** ESO controller ready, ExternalSecret ready, **ESO reports synced**
(promql on ESO's own Ready condition — no series means Prometheus is not scraping
ESO, a series pinned at 0 means ESO is telling you it cannot sync), both consumers
running, then four pending drill steps: **rotation performed** (the value in Vault
moved off the baseline), **rotation reached pod** (the file in the running
container equals Vault), **no redeploy needed**, **env consumer did not see it**
(the env-var pod is still on the old value, which is the lesson).

`no-redeploy-needed` is graded against a **checkpoint** the activation records —
the UID of the consumer pod and the Deployment's generation at the moment the
drill began — so passing means *this* pod, the one that was already running,
picked up a value it was never started with. Grading a literal
`observedGeneration == 1` instead would pass a pod that had been deleted and
recreated, since deleting a pod does not bump the Deployment's generation.

**Run the drill:**
- Watch the pod read the file: `kubectl -n <workload-ns> logs -l app=secret-consumer -f`
- Rotate in Vault: `kubectl -n vault exec vault-0 -- sh -c 'VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=root vault kv put secret/go-api api-key=rotated-v2'`
- Watch the new value appear in the log with no restart (~90s), then compare:
  `kubectl -n <workload-ns> exec deploy/secret-consumer -- cat /etc/api/api-key` against
  `kubectl -n <workload-ns> exec deploy/env-consumer -- sh -c 'echo $API_KEY'`
- Grade it: `labctl scenario verify secrets-management`

> **Two hops, not one:** ESO refreshes the Secret every 15s, then the kubelet
> refreshes the mounted file on its own cycle — end to end is up to ~90s, so a
> PENDING `rotation-reached-pod` for a minute is normal.

> **Env vars are the trap:** the kubelet cannot rewrite a running process's
> environment, so `env-consumer` stays on its start-time value until something
> rolls it. Mount a secret as a file when you want rotation to be free. (The key is
> bound with `secretKeyRef`, not `envFrom`: `api-key` is not a valid env var name
> and `envFrom` would skip it silently.)

> **ESO backs off, and re-seeding alone will not wake it:** dev-mode Vault keeps
> its KV in memory, so a `vault-0` restart empties it and every ESO read fails.
> The failures back the controller off exponentially — within a few hours it is
> retrying hours apart, not on its 15s `refreshInterval` — so putting the value
> back changes nothing until a reconcile is due. Break the backoff with ESO's own
> idiom, which activation now does for you:
> `kubectl -n <workload-ns> annotate externalsecret <workload>-secret force-sync=$(date +%s) --overwrite`

> **Reading ESO's metrics:** the `namespace` label on `externalsecret_*` is the
> *controller's* namespace (`external-secrets`); Prometheus moved the real one to
> `exported_namespace`. Select on `name`, or on `exported_namespace` — never on
> `namespace`. And a *successful* rotation looks like nothing on these metrics:
> ESO reconciles every 15s whether the value moved or not, so the dashboard is
> there for the failure shape. The last hop into the container's file is not
> instrumented at all; watch that in the consumer's logs.

> **No secrets in git:** the Vault dev token comes from `VAULT_DEV_ROOT_TOKEN` (default `root`).

---

### Day-2 Drill: Node Drain Under Load (`node-drain-drill`)

**Category:** operations

**What labctl stages:**
- The shape the drill needs: the workload scaled to 3 replicas, and the nodes
  currently holding its pods recorded so the drain can be graded
- A **Node Drain Drill** Grafana dashboard (`/d/node-drain-drill`): success rate,
  cordoned nodes, pod placement per node, ready vs desired replicas

**What you do:** write and apply the `PodDisruptionBudget`, start traffic, then
cordon and drain a worker node with `kubectl` and put it back. Nothing performs
the drain for you — draining is the transferable skill.

**Prerequisites:**
- Platform: ingress, monitoring/metrics
- Apps: the bound workload
- A multi-node cluster (k3d defaults to 2 agents; `AGENTS=2 labctl runtime up`)

**Checks (8):** workload ≥2 ready (guard) · PDB present · exactly one PDB selects
the workload · PDB tracks ≥2 healthy pods · traffic is flowing · a node that held
the workload now holds none · **success rate ≥ 99.5%** across the drain (promql) ·
no node left cordoned.

**Run the drill:**
- Apply your PDB: `labctl scenario render node-drain-drill manifests/baseline.yaml --app <app> | kubectl apply -f -`
- Start traffic: `labctl traffic start --profile steady --rps 20`
- See placement, then `kubectl cordon <node>` and
  `kubectl drain <node> --ignore-daemonsets --delete-emptydir-data`
- `kubectl uncordon <node>` and grade: `labctl scenario verify node-drain-drill`

> **Two traps this drill will walk you into, on purpose.**
> *Only one PDB may cover a pod.* The eviction API refuses a pod matched by two,
> and `kubectl drain` then fails partway through with an error naming neither —
> `chaos-engineering` and `cluster-upgrade-drill` both ship a budget for the same
> workload, so leaving one active collides. A check catches it before you drain.
> *Node-local storage does not move.* Prometheus, Loki and Alertmanager use
> `local-path` volumes, and a local PV is pinned to its node by node affinity, so
> draining that node leaves them `Pending` until you uncordon. That is why the
> drain is graded from cluster state rather than from a metric: the node you
> drain may be the one running the observer.

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

**Checks (8):** PDB present, PDB protects >=2 healthy, workload >=2 ready, all nodes
Ready and uncordoned (script), no node left behind on the old version (script),
workload survived the roll and is still spread across nodes (script), traffic was
actually flowing (promql), **success rate >= 99%** across the upgrade window
(promql).

**Run the drill:**
- Start traffic: `labctl traffic start --profile steady --rps 20`
- Apply your PDB: `labctl scenario render cluster-upgrade-drill manifests/baseline.yaml --app <app> | kubectl apply -f -`
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


> **Rolling a node used to break the cluster's load balancer — permanently.**
> k3d's `serverlb` holds its nginx upstreams as a fixed list of node names
> captured at cluster creation. `roll-node.sh` replaces a node under a *new* name
> (`agent-0` → `agent-0-0` → `agent-0-0-0`) and nothing updated that list. It is
> invisible while the cluster keeps running, because nginx is already up — but on
> the next restart confd re-renders the config, nginx fails validation on the
> missing host (`[emerg] host not found in upstream "k3d-snowops-agent-0:443"`)
> and the load balancer never starts. One dead upstream takes down the whole
> listener set **including the 6443 mapping the kubeconfig points at**, so the
> entire lab — API server included — is unreachable. Observed for real on
> 2026-09-09 after a Docker restart. `roll-node.sh` now repoints the load
> balancer and restarts it; the rewrite matches whole lines, because node names
> are prefixes of one another.



**Checks (8).** Four are the drill and stay PENDING until it is done:
`no-node-left-behind`, `workload-survived-the-roll`, `traffic-is-flowing` and
`availability-held-during-upgrade`.

> **A cluster nobody touched passes every version test there is.** One distinct
> kubelet version across every node is what "finished" and "never started" both
> look like, so `no-node-left-behind` used to be green before the drill began.
> Activation now records the workers **by UID** and the check refuses to call the
> roll finished while any of them is still the same node. UID, not name: k3d
> reuses a node's name whenever it can, so a genuinely rolled worker usually
> comes back called exactly what it was.

> **The availability grade was a ratio of two rates, which reads 1.00 on an idle
> cluster.** The scenario's headline number was therefore green on an untouched
> lab — sitting next to a `traffic-is-flowing` check correctly reporting there
> was nothing to measure. It is now a script that requires a roll to have
> started, requires real load, and measures from the roll rather than a fixed ten
> minutes back. It also clamps the window to what Prometheus can actually see,
> because this drill can destroy Prometheus's own volume.

> **`roll-node.sh` failed whenever k3d reused the node name.** It identified the
> replacement by diffing node *names* before and after, so when the name came
> back unchanged the diff was empty and it died with "the replacement node did
> not register with the API server" — about a node that had registered perfectly.
> It now diffs UIDs, polls for registration instead of reading once, and no
> longer swallows the `k3d node create` error behind `|| true`.

> **The stranded-PVC recovery did not recover the common case.** It looked for
> volumes pinned to a node that no longer exists. When the name is reused the
> affinity still matches, the pod schedules happily and then cannot mount —
> `MountVolume.NewMounter initialization failed ... path ... does not exist` —
> and the script reported "No stranded volumes" while three pods sat stuck for
> ever. It now also detects that case from the kubelet's own FailedMount events.
> Verified live: it found and released Prometheus, Alertmanager and Loki after a
> real roll.

> **Rolling the node that holds your observability stack is part of the lesson.**
> Prometheus, Loki and Alertmanager use `local-path` volumes pinned to a node; if
> that node is the one you roll, their data goes with it. Run
> `reclaim-stranded-pvcs.sh` (with `DRY_RUN=true` first), and expect the metrics
> history — not the lab — to be gone.

> **A drain will block on someone else's PodDisruptionBudget.** Confirmed live:
> the drain stalled for the full five-minute timeout on single-replica `istiod`,
> whose PDB forbids evicting its last pod. `kubectl get pdb -A -o wide` and look
> for ALLOWED DISRUPTIONS 0. This is the drain working correctly and the most
> common reason a real node roll stalls.

---

### Day-2 Drill: Namespace Backup & Restore (`backup-restore-drill`)

**Category:** operations

**What it deploys:**
- A `restore-marker` ConfigMap in the workload namespace carrying a **token
  minted at activation**. The token lives in exactly two places — the cluster and
  your archive — which is what makes a restore distinguishable from a re-create
- A `data-writer` Deployment mounting a PVC (`restore-data`) that persists a
  random `boot-id` on its PersistentVolume, so PV-data loss is **measured**, not
  just described
- A Grafana dashboard (`/d/backup-restore`) plotting the object count through the
  loss and the restore, and the **age of the volume behind the claim** — which
  drops to zero the moment a restore provisions a replacement

**Prerequisites:**
- Apps: the bound workload
- `jq`, which does the scrubbing. `labctl doctor` lists it alongside the rest of
  the toolchain; it is optional there because the lab stands up without it

**Checks (8):** namespace exists, workload running, data-writer running, **backup
archive usable**, **loss was observed**, **restore round-tripped**, restore PVC
present, **PV data loss demonstrated**. The last five are `pending` — they render
as PENDING, not FAIL, until the matching drill step is done. Three of them grade
something live state alone cannot see:

- *usable*, not merely present: the archive is parsed, and must contain the
  restore marker with the current token. An empty file created by `touch` fails.
- *loss was observed*: nothing in a healthy namespace distinguishes "I restored
  it" from "I never broke it", so the loss has to be witnessed. Every verify that
  finds the marker missing records it; that run is the evidence.
- *round-tripped*: the live marker's token must equal the archive's.
  `labctl scenario up --force` mints a new token and turns this red, because
  re-staging is not a restore.

**Run the drill (real `kubectl` — the commands you would use on a live cluster):**
- Back up: `bash scenarios/backup-restore-drill/scripts/backup.sh <workload-ns>`
  (read it — it is `kubectl get … -o json | jq <scrub>`). It also records the
  data-writer's `boot-id`: the fingerprint of the data the archive does *not*
  contain.
- Simulate loss: `kubectl -n <workload-ns> delete configmap restore-marker`
- **Verify while it is lost** — that run is what grades the loss
- Restore: `kubectl apply --server-side --force-conflicts -f .labctl/backups/<workload-ns>-latest.json`
- Then the hard half: `kubectl -n <workload-ns> delete deploy data-writer &&
  kubectl -n <workload-ns> delete pvc restore-data`, restore again, and run
  `observe-pv-data.sh` — the `boot-id` changed.

> **Manifest-level backup:** archives round-trip Kubernetes objects, not
> PersistentVolume data. Delete the ConfigMap and the `boot-id` survives a
> restore; delete the claim and it comes back bound to a brand-new empty volume.
> The object round-trips, the data does not. For stateful data use a volume
> snapshot or Velero with restic.

> **Deleting the whole namespace** also works — `restore.sh` recreates it before
> applying — but the archive deliberately skips Helm release Secrets, so the
> workload comes back as loose objects with no release behind them. Finish with
> `labctl app deploy <workload>` to put it back under management.

> **Teardown removes the archives** along with the marker. They are keyed to a
> token that teardown destroys, so a kept archive would only grade red next time.

---

### Cost & Capacity: Right-Sizing (`cost-right-sizing`)

**Category:** cost

**The exercise:** the bound workload arrives reserving 2000m CPU and 1Gi of
memory — roughly forty times what it uses. Drive real traffic, read the gap
between reserved and used, then right-size the requests.

**What it stages:**
- `inflate.sh`, which puts the workload into the over-provisioned state with one
  `kubectl set resources` — the exact inverse of the fix — and records the sizing
  the workload arrived with in an annotation so teardown can restore it. It
  mutates the running Deployment directly rather than via a Helm upgrade, so it
  works no matter how the workload was deployed.
- A ServiceMonitor for OpenCost, so its cost figures reach Prometheus instead of
  living only inside its own UI
- A dashboard plotting requests against usage, CPU efficiency, OpenCost
  allocation and throttling

**Checks (8):** four guard the environment (workload running, OpenCost running,
cost metrics scraped, `/health` returns 200). Four grade the drill: the workload
is under real load, the CPU request is right-sized, the memory request is
right-sized, and the workload is not being throttled.

**Grading has two sides, and that is the point.** A request over the waste
ceiling fails, and so does one *under* the peak the workload actually reached.
An upper bound alone makes `--requests=cpu=1m` a full-marks answer to a scenario
about sizing requests correctly.

**Traps this scenario encodes:**
- **Sizing an idle workload is not right-sizing.** With no traffic the measured
  peak collapses to a couple of millicores and an absurd request clears the
  floor. `workload-under-load` is graded before the sizing checks for that reason.
- **The floor is a peak over a window, not a spot reading.** It comes from
  Prometheus, not `kubectl top`: a sample taken while the workload happened to be
  quiet blesses a request that throttles the moment traffic returns, and
  metrics-server is briefly blind to a pod that has just rolled — exactly when the
  learner re-runs verify after right-sizing.
- **Requests bill; limits throttle.** `workload-not-throttled` catches the learner
  who cuts both to the same small number, capping the workload below what it
  needs rather than saving anything.
- **Prometheus overwrites a target's exported labels.** OpenCost reports one
  series per container in the cluster, carrying that container's own namespace
  and pod labels. Without `honorLabels: true` every cost series comes back
  labelled `namespace="opencost"` and every per-namespace cost query returns
  nothing while the data is plainly there.
- **A dotted annotation key needs escaping in jsonpath.** `snowops.net/...` reads
  as nested fields unless the dots are escaped, so the restore read nothing and
  set the shared workload's requests and limits to zero on teardown.

**Prerequisites:**
- Platform: cost/opencost, monitoring/metrics, ingress
- Apps: the bound workload

> `cost/opencost` needs `monitoring/metrics` (Prometheus + kube-state-metrics) to
> compute allocation — without it the OpenCost API returns no cost data. `scenario
> up` warns if a platform prerequisite is missing and prints the `labctl platform
> up <component>` command to install it.

**The drill:**

1. `labctl scenario up cost-right-sizing` — inflates the requests; the two sizing
   checks report **pending**
2. `labctl traffic start --profile steady --rps 25` — without load the peak is
   meaningless and the floor cannot bite
3. Read the gap on `http://grafana.k3d.local/d/cost-right-sizing`, and the cost in
   the OpenCost UI at `http://opencost.k3d.local` (`labctl hosts add` once;
   otherwise `kubectl -n opencost port-forward svc/opencost 9090 &`)
4. Right-size above the observed peak and under the ceiling:
   `kubectl -n <workload-ns> set resources deployment <workload> --requests=cpu=<peak+headroom>,memory=<peak+headroom>`
5. `labctl scenario verify cost-right-sizing` — all checks **pass**
6. `labctl scenario down cost-right-sizing` restores the sizing the workload
   arrived with; it is shared with every other scenario

> **k3d note:** no real billing API — OpenCost uses on-prem pricing defaults
> (~$0.048/CPU-hr). Cost numbers are relative; the before/after contrast is real.

---

Full YAML reference: [scenario schema](reference/scenario-schema.md).
Sharing scenarios from your own repository, and the security implications of
doing so, are covered there too.
