# Solution — noisy-neighbor

## What happened

A batch workload — `report-batch`, in the `labfault-batch` namespace — was
scheduled onto the same node as {{.WorkloadName}}. Each replica requests
`cpu: 500m`, sets **no CPU limit**, and runs a hot loop. There is one replica
per core on the node, so the node sits at ~90% CPU while its two siblings idle.

Two things follow, and only the second is obvious:

- **The request buys priority.** The Linux scheduler hands out contended CPU in
  proportion to `requests`, so `500m` per burner against {{.WorkloadName}}'s
  `50m` is a ten-to-one claim on every spare cycle, per replica.
- **The missing limit removes the ceiling.** With a limit, a container is
  throttled at its own number no matter what else is idle. Without one, it takes
  everything nobody is actively using — which, on a busy node, is everything.
  Each burner draws about 1000m against a 500m request.

## Why {{.WorkloadName}} is still serving

This is the part worth keeping. {{.WorkloadName}} declares
`requests.cpu: 50m`, and a request is a *guarantee*: under contention CFS still
hands it its proportional share, so at the traffic this lab generates it never
waits and its latency does not move. The workload is protected because someone
sized it.

What it has lost is **headroom**. Its limit is `200m`, and on a contended node
its share works out at roughly `97m` — so it can no longer burst to the ceiling
it was given. A traffic spike, a failover, a neighbour restarting: any of those
now land on a node with nothing left to give. A workload on that node with *no*
CPU request at all would already be in trouble.

That is the real cost of a noisy neighbour on a well-sized cluster. Not an
outage — a silent loss of the margin you thought you had.

## Diagnosis path

```bash
kubectl -n {{.WorkloadNamespace}} get pods -o wide   # which node is it on?
kubectl top nodes                                     # one node at capacity, two idle
kubectl top pods -A --sort-by=cpu                     # the batch pods are on top
kubectl -n labfault-batch get deploy report-batch -o yaml
# requests: cpu 500m, no limits block at all, and a `while true` loop
```

The page you were sent — `LabFaultNoisyNeighbor` — names the node and nothing
else, which is what a real saturation alert can tell you. Finding the tenant is
the work.

## Fix

```bash
kubectl -n labfault-batch set resources deploy/report-batch \
  --requests=cpu=100m --limits=cpu=200m
```

The limit must be at least the request, so capping means lowering both — which
is the real fix for a workload that over-requested and never bounded itself.
Recovery is immediate: the burners are throttled the moment the new pods
schedule, and the node drops back to normal within a scrape or two.

## Accepted fixes

The detection check grades the **contention**, not the object, so any of these
resolves the incident:

```bash
# bound it — the answer that generalises
kubectl -n labfault-batch set resources deploy/report-batch \
  --requests=cpu=100m --limits=cpu=200m

# or stop it consuming without removing it
kubectl -n labfault-batch scale deploy report-batch --replicas=0

# or evict it outright
kubectl delete namespace labfault-batch
```

Capping is the answer you would give in production, and it leaves the tenant
running — which means the lab still has it once the incident closes, and
`labctl incident resolve` with no argument will refuse because nothing is
active any more. Clear it by name when you are done:

```bash
labctl incident resolve noisy-neighbor
```

What the check will **not** accept is a limit large enough to be no limit at
all. `--limits=cpu=4` on a four-core node satisfies "the pod has a limit" while
changing nothing about who gets the machine, so the check requires the pods'
limits to add up to less than half the node.

## Real-world parallel

On shared clusters this is a *policy* failure more than a workload one. No
review of the batch team's Deployment would have caught it, because their
Deployment is fine in isolation — it is only antisocial in company.

The guard rails that actually prevent it live on the namespace, not the pod:

- **`LimitRange`** supplies a default CPU limit to every pod admitted without
  one. This incident cannot happen in a namespace that has one.

  Retrofitting one onto a namespace that is *already* misbehaving has a trap
  worth knowing: the default limit must be at least as large as what the pods
  already request, or admission rejects every new pod with
  `requests: Invalid value: "500m": must be less than or equal to cpu limit of
  200m`. The Deployment then wedges on `FailedCreate` while the old, uncapped
  pods keep running — the namespace looks governed and nothing has changed.
  Lower the requests first, or set the default above them and tighten later.
- **`ResourceQuota`** caps a tenant's total, so a runaway replica count cannot
  consume a node even with limits set.
- **Admission policy** — the security-compliance scenario's Kyverno
  `require-resource-limits` rule rejects the Deployment outright. Activate that
  scenario and inject this fault again: the policy now has something to say.
