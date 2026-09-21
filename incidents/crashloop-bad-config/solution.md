# Solution — crashloop-bad-config

## What happened

The {{.WorkloadName}} Deployment's container `command` was overridden with
`/bin/false`, so every new pod exits immediately with code 1 and enters
CrashLoopBackOff. Because Deployments roll out progressively, the old
ReplicaSet's pods keep serving — the symptom is a *stuck rollout*, not a
full outage.

## Diagnosis path

```bash
kubectl get pods -n {{.WorkloadNamespace}}                      # new pods CrashLoopBackOff, old pod Running
kubectl rollout status deploy/{{.WorkloadName}} -n {{.WorkloadNamespace}}  # "Waiting for deployment ... to finish" — stuck
kubectl describe pod -n {{.WorkloadNamespace}} <crashing-pod>   # Last State: Terminated, Exit Code 1, no app logs
kubectl get deploy {{.WorkloadName}} -n {{.WorkloadNamespace}} -o jsonpath='{.spec.template.spec.containers[0].command}'
# ["/bin/false"]  ← there's your problem
```

## Fix

Remove the command override and let the image's own entrypoint run:

```bash
kubectl -n {{.WorkloadNamespace}} patch deploy {{.WorkloadName}} --type=json \
  -p '[{"op":"remove","path":"/spec/template/spec/containers/0/command"}]'
kubectl -n {{.WorkloadNamespace}} rollout status deploy/{{.WorkloadName}}
```

Verify: `labctl incident status` — the detection check passes once the
rollout completes.

## Real-world parallel

Bad entrypoint/command overrides ship constantly: a debug command left in a
values file, a wrong `args` merge, an init wrapper missing from the image.
The lesson: when pods die with exit code 1 *before logging*, read the spec,
not just the logs.
