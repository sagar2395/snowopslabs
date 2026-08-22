# Solution — oom-kill

## What happened

echo-server's memory limit was reduced to 16Mi (request 8Mi). The Go
runtime needs more than that just to start, so the kernel OOM-kills the
container immediately — exit code 137, `Reason: OOMKilled` — and the pod
crash-loops.

## Diagnosis path

```bash
kubectl get pods -n echo-server                 # RESTARTS climbing
kubectl describe pod -n echo-server <pod>       # Last State: OOMKilled, Exit Code 137
kubectl get deploy echo-server -n echo-server \
  -o jsonpath='{.spec.template.spec.containers[0].resources}'
# {"limits":{"memory":"16Mi"},"requests":{"memory":"8Mi"}}
```

In Grafana: the pod memory panel shows usage slamming into a flat ceiling
right before each restart.

## Fix

```bash
kubectl -n echo-server set resources deploy/echo-server \
  --limits=memory=256Mi --requests=memory=64Mi
kubectl -n echo-server rollout status deploy/echo-server
```

(The pre-fault values are recorded in the `labfault-oom-kill-original-*`
annotations on the deployment.)

## Real-world parallel

Memory limits get tightened during "cost optimization" passes, or a new
library raises the baseline footprint past an old limit. Exit code 137 is
the signature — always check `Last State` before reading app logs. Set
limits from observed usage plus headroom, and alert on
`kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}`.
