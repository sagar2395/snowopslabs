# Solution — oom-kill

## What happened

{{.WorkloadName}}'s memory limit was cut to a quarter of its own memory request.
That is deliberately above what the app uses at rest and below what it needs
under load, so the pod starts healthy and is OOM-killed — exit code 137,
`Reason: OOMKilled` — only once traffic arrives. The limit is derived from the
workload rather than hardcoded, because a number that squeezes a small Go
service would stop a JVM from ever starting.

## Diagnosis path

```bash
kubectl get pods -n {{.WorkloadNamespace}}                 # RESTARTS climbing
kubectl describe pod -n {{.WorkloadNamespace}} <pod>       # Last State: OOMKilled, Exit Code 137
kubectl get deploy {{.WorkloadName}} -n {{.WorkloadNamespace}} \
  -o jsonpath='{.spec.template.spec.containers[0].resources}'
# the limit will read as a quarter of the request below it
```

In Grafana: the pod memory panel shows usage slamming into a flat ceiling
right before each restart.

## Fix

```bash
kubectl -n {{.WorkloadNamespace}} set resources deploy/{{.WorkloadName}} \
  --limits=memory=<above the peak you measured> --requests=memory=<the original request>
kubectl -n {{.WorkloadNamespace}} rollout status deploy/{{.WorkloadName}}
```

(The pre-fault values are recorded in the `labfault-oom-kill-original-*`
annotations on the deployment.)

The fault runs the k6 generator to create the load that makes the limit bite.
Fixing the limit closes the incident without running `resolve.sh`, so stop the
load yourself when you are done:

```bash
labctl traffic stop
```

## Real-world parallel

This is the shape the failure really takes: the pod is perfectly healthy at
rest and dies only once traffic arrives, so it passes every check in a quiet
environment and falls over in production. Memory limits get tightened during
"cost optimization" passes, or a new library raises the baseline footprint past
an old limit. Exit code 137 is
the signature — always check `Last State` before reading app logs. Set
limits from observed usage plus headroom, and alert on
`kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}`.

## How high is too high

Raising the limit until the kills stop is the fix; raising it to 4Gi "to be
safe" is not. The detection check accepts it — the container genuinely is no
longer being killed, and grading over-provisioning here would be grading a
different lesson — but you have only moved the problem.

A limit far above what the workload uses, sitting next to a request far below
it, is how a node gets overcommitted: the scheduler packs by *request* and
believes there is room, while the pods are free to grow into memory the node
does not have. The first one to actually use its limit takes the node's other
tenants with it.

Set the limit from the peak you measured under load, plus headroom you can
justify — and move the request up with it, so the scheduler is told the truth.
The `cost-right-sizing` scenario is the same question asked deliberately.
