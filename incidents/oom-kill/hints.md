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
The limit is 8Mi. That is enough to idle on — which is why nothing looked
wrong until traffic arrived — but not enough to serve requests. Compare it
with what the app actually needs under load (Grafana's container memory
panels, or `kubectl top pod -n echo-server` while the load is running) and
raise it:
`kubectl -n echo-server set resources deploy/echo-server --limits=memory=256Mi --requests=memory=64Mi`.
