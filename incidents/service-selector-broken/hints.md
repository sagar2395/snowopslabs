# Hints — service-selector-broken

## Hint 1
503 from the ingress usually means the ingress had nowhere to send the
request. Pods look fine, so check the layer between ingress and pods: the
Service. Is it actually fronting anything?

## Hint 2
`kubectl get endpoints go-api -n go-api` — `<none>`. A Service with no
endpoints matches no pods. Endpoints come from the Service's
**selector** matching pod **labels**. Compare them.

## Hint 3
`kubectl get svc go-api -n go-api -o jsonpath='{.spec.selector}'` vs
`kubectl get pods -n go-api --show-labels`. The selector says
`app.kubernetes.io/name=labfault-nobody`; the pods say
`app.kubernetes.io/name=go-api`. Patch the selector back and watch the
endpoints repopulate.
