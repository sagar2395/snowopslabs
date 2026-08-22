# Solution — network-blackhole

## What happened

A NetworkPolicy named `labfault-network-blackhole` was applied to the
`go-api` namespace with an empty `podSelector` (matches all pods),
`policyTypes: [Ingress]`, and no ingress rules — the canonical
deny-all-ingress policy. k3s enforces NetworkPolicies out of the box, so
all traffic *to* the pods is dropped, including from the ingress
controller. The pods themselves never notice.

## Diagnosis path

```bash
curl -v http://go-api.k3d.local/health            # times out / 5xx from traefik
kubectl get pods -n go-api                        # all Running, Ready
kubectl get endpoints go-api -n go-api            # endpoints populated — Service is fine
kubectl port-forward -n go-api deploy/go-api 8080:8080 &
curl localhost:8080/health                        # works! pod is healthy
kubectl get networkpolicy -n go-api               # ← there it is
kubectl describe networkpolicy labfault-network-blackhole -n go-api
```

## Fix

```bash
kubectl delete networkpolicy labfault-network-blackhole -n go-api
```

Recovery is immediate — no restart needed.

## Real-world parallel

Deny-all policies are a security best practice — *when paired with the
allow rules that go with them*. A namespace-wide deny applied without its
companion allows (or applied to the wrong namespace by a templating bug)
produces exactly this: a total outage with every health indicator green.
When app-level signals look perfect but traffic dies, check the network
*policy* layer before the network itself.
