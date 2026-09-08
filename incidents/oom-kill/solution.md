# Solution — oom-kill

## What happened

{{.WorkloadName}}'s memory limit was reduced to 16Mi (request 8Mi). The Go
runtime needs more than that just to start, so the kernel OOM-kills the
container immediately — exit code 137, `Reason: OOMKilled` — and the pod
crash-loops.

## Diagnosis path

```bash
kubectl get pods -n {{.WorkloadNamespace}}                 # RESTARTS climbing
kubectl describe pod -n {{.WorkloadNamespace}} <pod>       # Last State: OOMKilled, Exit Code 137
kubectl get deploy {{.WorkloadName}} -n {{.WorkloadNamespace}} \
  -o jsonpath='{.spec.template.spec.containers[0].resources}'
# {"limits":{"memory":"16Mi"},"requests":{"memory":"8Mi"}}
```

In Grafana: the pod memory panel shows usage slamming into a flat ceiling
right before each restart.

## Fix

```bash
kubectl -n {{.WorkloadNamespace}} set resources deploy/{{.WorkloadName}} \
  --limits=memory=256Mi --requests=memory=64Mi
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
