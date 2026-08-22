# Hints — oom-kill

## Hint 1
The pods are restarting on a loop. `kubectl get pods -n echo-server` shows
climbing RESTARTS — but *why* are they dying? `kubectl describe pod` and
read the container's **Last State** carefully.

## Hint 2
`Last State: Terminated, Reason: OOMKilled, Exit Code: 137`. The kernel is
killing the container for exceeding its memory limit. So what *is* the
limit? Check the deployment's `resources` block.

## Hint 3
A 16Mi limit can't even hold a Go runtime. Compare with what the app
actually needs (Grafana's container memory panels, or
`kubectl top pod -n echo-server` on a healthy replica) and raise it:
`kubectl -n echo-server set resources deploy/echo-server --limits=memory=256Mi --requests=memory=64Mi`.
