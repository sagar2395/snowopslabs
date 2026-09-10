# Hints — service-selector-broken

## Hint 1
The page named the Service, not a pod — and 503 from an ingress usually means
it had nowhere to send the request. Pods look fine, so check the layer between
ingress and pods. Is the Service actually fronting anything?

## Hint 2
`kubectl get endpoints {{.WorkloadName}} -n {{.WorkloadNamespace}}` — `<none>`. A Service with no
endpoints matches no pods. Endpoints come from the Service's
**selector** matching pod **labels**. Compare them.

## Hint 3
`kubectl get svc {{.WorkloadName}} -n {{.WorkloadNamespace}} -o jsonpath='{.spec.selector}'` vs
`kubectl get pods -n {{.WorkloadNamespace}} --show-labels`. They disagree on
`app.kubernetes.io/name` — the Service is looking for a value the pods do not
carry, and a selector is an AND, so one wrong key matches nothing.

Fix it on the **Service**, not the pods. Relabelling a pod to match makes the
503s stop and leaves the Deployment still stamping the other label on every pod
it creates, so the next rollout breaks it again.
