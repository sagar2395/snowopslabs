# Hints — noisy-neighbor

## Hint 1
The page named a *node*, not a workload, so stop looking inside
{{.WorkloadName}}. Start by confirming what the page said, and notice that the
other nodes are idle — whatever this is, it is in one place.

```bash
kubectl -n {{.WorkloadNamespace}} get pods -o wide
kubectl top nodes
```

## Hint 2
The node is full, and {{.WorkloadName}} is only a small part of it. Something
else scheduled there is taking the rest. Rank every pod on the cluster by CPU
and look at what is on top — then at what its Deployment asks for.

```bash
kubectl top pods -A --sort-by=cpu
```

## Hint 3
`labfault-batch/report-batch` requests 500m per replica and sets **no CPU
limit**. The request bought it a large share of the node's cycles; the missing
limit means nothing stops it taking the rest, so each replica draws about double
what it reserved. {{.WorkloadName}} keeps serving — its own request guarantees
it a share — but it has lost the headroom to burst to the limit it was given.

Bound it, scale it away, or evict it. Then ask the question that matters: what
would have stopped this being possible in the first place?
