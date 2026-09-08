# Solution — service-selector-broken

## What happened

The {{.WorkloadName}} Service's selector was changed to
`app.kubernetes.io/name: labfault-nobody`, which matches no pod. The
endpoints object emptied out, so the ingress controller has no backend —
503 for every request, while pods, probes, and logs all stay green.

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

## Real-world parallel

Selector/label drift is a classic silent killer: a Helm refactor renames
the selector labels, a copy-pasted Service ships with the wrong app name,
or a label is "cleaned up" on the deployment. Selectors are immutable on
Deployments but *mutable on Services*, so this slips through. Empty
endpoints with healthy pods is the fingerprint — check
`kubectl get endpoints` before blaming the app or the ingress.
