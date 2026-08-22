# Hints — noisy-neighbor

## Hint 1
go-api didn't change, but the *node* it runs on did. Look at cluster-level
resource usage: `kubectl top nodes` (or the node CPU panel in Grafana).
Who is using all that CPU?

## Hint 2
`kubectl top pods -A --sort-by=cpu` ranks every pod on the cluster by CPU.
The top entries aren't yours. Which namespace are they in, and what do
their resource requests/limits look like?

## Hint 3
A deployment with `requests.cpu: 500m` per replica and **no CPU limit** in
the `labfault-noisy-neighbor` namespace is burning everything it can grab.
Evict it (`kubectl delete namespace labfault-noisy-neighbor`) — and think
about what would have prevented this: limits, quotas, or LimitRanges.
