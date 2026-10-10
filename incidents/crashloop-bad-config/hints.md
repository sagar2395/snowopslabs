# Hints — crashloop-bad-config

## Hint 1
Users get errors on every request, so start from what should be answering them:
`kubectl get pods -n {{.WorkloadNamespace}}` and read the STATUS and RESTARTS
columns. In Grafana, *Pod Resources* shows the same thing over time:
*Container restarts* and *Containers not running, by reason*. Is anything left
serving?

## Hint 2
CrashLoopBackOff only says the container keeps exiting, not why. A container
that crashed already wrote its last words: read them with
`kubectl logs -n {{.WorkloadNamespace}} <crashing-pod> --previous`, and check
Last State and Exit Code in `kubectl describe pod`. What does the error
complain about?

## Hint 3
The error names a setting the app reads when it starts. Settings like that come
from the pod template, not the pod:
`kubectl get deploy {{.WorkloadName}} -n {{.WorkloadNamespace}} -o yaml` and
read the container's `env`. Compare it with the `containerPort` a few lines
above. Fix the Deployment, not the pod: a deleted pod comes back from the same
template and crashes the same way.
