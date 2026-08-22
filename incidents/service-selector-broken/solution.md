# Solution — service-selector-broken

## What happened

The go-api Service's selector was changed to
`app.kubernetes.io/name: labfault-nobody`, which matches no pod. The
endpoints object emptied out, so the ingress controller has no backend —
503 for every request, while pods, probes, and logs all stay green.

## Diagnosis path

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://go-api.k3d.local/health   # 503
kubectl get pods -n go-api                       # Running, Ready — fine
kubectl get endpoints go-api -n go-api           # ENDPOINTS: <none>  ← the tell
kubectl get svc go-api -n go-api -o jsonpath='{.spec.selector}'
kubectl get pods -n go-api --show-labels         # labels don't match the selector
```

## Fix

```bash
kubectl -n go-api patch svc go-api \
  -p '{"spec":{"selector":{"app.kubernetes.io/name":"go-api"}}}'
kubectl get endpoints go-api -n go-api           # endpoints back
```

## Real-world parallel

Selector/label drift is a classic silent killer: a Helm refactor renames
the selector labels, a copy-pasted Service ships with the wrong app name,
or a label is "cleaned up" on the deployment. Selectors are immutable on
Deployments but *mutable on Services*, so this slips through. Empty
endpoints with healthy pods is the fingerprint — check
`kubectl get endpoints` before blaming the app or the ingress.
