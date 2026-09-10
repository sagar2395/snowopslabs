# Hints — network-blackhole

## Hint 1
The page came from the edge, not from the app — and the app says it is fine:
pods Running, probes green, logs quiet. Nothing is even reaching it to be
logged. Trust the app and suspect the path to it: walk the request hop by hop,
ingress → Service → Endpoints → pod, and find where it dies.

## Hint 2
`kubectl get endpoints {{.WorkloadName}} -n {{.WorkloadNamespace}}` shows healthy endpoints, and
`kubectl port-forward` straight to a pod works. So the pod is reachable —
but not *through the network path*. What Kubernetes objects can silently
drop traffic between two healthy points?

## Hint 3
`kubectl get networkpolicy -n {{.WorkloadNamespace}}`. A policy with `podSelector: {}`,
`policyTypes: [Ingress]`, and **no rules** means "select every pod, allow
no ingress" — a deny-all. Delete it and the service comes back instantly.
