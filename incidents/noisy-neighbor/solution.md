# Solution — noisy-neighbor

## What happened

A deployment of CPU-burning pods (busybox hot loops) landed in the
`labfault-noisy-neighbor` namespace with `requests.cpu: 500m` each and no
CPU limit. The requests reserve scheduler capacity; the missing limit lets
the pods consume every spare cycle. Co-located workloads — go-api included
— see higher latency under load even though nothing about them changed.

## Diagnosis path

```bash
kubectl top nodes                          # CPU pegged
kubectl top pods -A --sort-by=cpu          # noisy-neighbor pods on top
kubectl get deploy -n labfault-noisy-neighbor noisy-neighbor -o yaml
# requests but no limits, and a `while true` loop as the command
```

In Grafana: node CPU saturated, go-api p99 latency elevated while its own
CPU usage is throttled by competition.

## Fix

```bash
kubectl delete namespace labfault-noisy-neighbor
```

Recovery is immediate.

## Real-world parallel

On shared clusters this is a *policy* failure more than a workload one.
The guard rails that prevent it: `LimitRange` (default limits per
namespace), `ResourceQuota` (caps per tenant), and admission policies
(e.g. the security scenario's Kyverno `require-resource-limits`). Try
activating the security-compliance scenario and injecting this fault again
— the policy now has something to say about it.
