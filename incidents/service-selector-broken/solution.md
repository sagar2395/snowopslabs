# Solution — service-selector-broken

## What happened

The {{.WorkloadName}} Service's selector was changed to
`app.kubernetes.io/name: {{.WorkloadName}}-v2` — a rename that reached the
Service and never reached the Deployment. It matches no pod, the endpoints
object emptied out, and the ingress controller has no backend: 503 for every
request, while pods, probes, and logs all stay green.

Nothing reports this. Kubernetes emits no event when a selector stops matching,
because nothing is failing — the Service is doing exactly what it was told. The
only tell is that `kubectl get endpoints` is empty, which you have to think to
look at.

## Diagnosis path

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://{{.WorkloadName}}.{{.DomainSuffix}}/health   # 503
kubectl get pods -n {{.WorkloadNamespace}}                       # Running, Ready — fine
kubectl get endpoints {{.WorkloadName}} -n {{.WorkloadNamespace}}           # ENDPOINTS: <none>  ← the tell
kubectl get svc {{.WorkloadName}} -n {{.WorkloadNamespace}} -o jsonpath='{.spec.selector}'
kubectl get pods -n {{.WorkloadNamespace}} --show-labels         # labels don't match the selector
```

## Fix

```bash
kubectl -n {{.WorkloadNamespace}} patch svc {{.WorkloadName}} \
  -p '{"spec":{"selector":{"app.kubernetes.io/name":"{{.WorkloadName}}"}}}'
kubectl get endpoints {{.WorkloadName}} -n {{.WorkloadNamespace}}           # endpoints back
```

## Fix it on the Service, not the pods

`kubectl label pod ... app.kubernetes.io/name={{.WorkloadName}}-v2` also makes
the 503s stop, and it is the wrong fix. The Deployment's pod template still
stamps the old label on everything it creates, so the app breaks again at the
next rollout — and the person who does that rollout will not connect it to this.
The detection check refuses that route for the same reason: it requires the
Service's selector to be satisfied by the Deployment's own pod template, not by
whatever labels happen to be on a pod right now.

## Real-world parallel

Selector/label drift is a classic silent killer: a Helm refactor renames
the selector labels, a copy-pasted Service ships with the wrong app name,
or a label is "cleaned up" on the deployment. Selectors are immutable on
Deployments but *mutable on Services*, so this slips through. Empty
endpoints with healthy pods is the fingerprint — check
`kubectl get endpoints` before blaming the app or the ingress.
