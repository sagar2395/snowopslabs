# Hints — network-blackhole

## Hint 1
The app says it's fine: pods Running, probes green, logs quiet. So trust
the app and suspect the path to it. Walk the request hop by hop: ingress →
Service → Endpoints → pod. Where does it die?

## Hint 2
`kubectl get endpoints go-api -n go-api` shows healthy endpoints, and
`kubectl port-forward` straight to a pod works. So the pod is reachable —
but not *through the network path*. What Kubernetes objects can silently
drop traffic between two healthy points?

## Hint 3
`kubectl get networkpolicy -n go-api`. A policy with `podSelector: {}`,
`policyTypes: [Ingress]`, and **no rules** means "select every pod, allow
no ingress" — a deny-all. Delete it and the service comes back instantly.
