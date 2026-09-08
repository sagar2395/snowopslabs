# Hints — service-selector-broken

## Hint 1
503 from the ingress usually means the ingress had nowhere to send the
request. Pods look fine, so check the layer between ingress and pods: the
Service. Is it actually fronting anything?

## Hint 2
`kubectl get endpoints {{.WorkloadName}} -n {{.WorkloadNamespace}}` — `<none>`. A Service with no
endpoints matches no pods. Endpoints come from the Service's
**selector** matching pod **labels**. Compare them.

## Hint 3
`kubectl get svc {{.WorkloadName}} -n {{.WorkloadNamespace}} -o jsonpath='{.spec.selector}'` vs
`kubectl get pods -n {{.WorkloadNamespace}} --show-labels`. The selector says
`app.kubernetes.io/name=labfault-nobody`; the pods say
`app.kubernetes.io/name={{.WorkloadName}}`. Patch the selector back and watch the
endpoints repopulate.
