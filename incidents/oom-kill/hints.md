# Hints — oom-kill

## Hint 1
The pods are restarting on a loop. `kubectl get pods -n {{.WorkloadNamespace}}` shows
climbing RESTARTS — but *why* are they dying? `kubectl describe pod` and
read the container's **Last State** carefully.

## Hint 2
`Last State: Terminated, Reason: OOMKilled, Exit Code: 137`. The kernel is
killing the container for exceeding its memory limit. So what *is* the
limit? Check the deployment's `resources` block.

## Hint 3
The limit was cut to just above what the container uses at rest — enough to
start on and idle on, which is why nothing looked wrong for the first minute,
but not enough to serve requests. Read the number off the deployment itself rather
than assuming one, then compare it with what the app actually uses under load
(Grafana's container memory panels, or `kubectl top pod -n {{.WorkloadNamespace}}`
while the load is running) and raise the limit above that peak.
