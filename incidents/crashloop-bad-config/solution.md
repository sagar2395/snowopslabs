# Solution — crashloop-bad-config

## What happened

A config change set the {{.WorkloadName}} container's `PORT` environment
variable to `80800`, one zero too many. The app reads `PORT` at startup, cannot
listen on a port that does not exist, logs the error and exits with code 1. The
kubelet restarts it, it fails again, and the waits between restarts double:
CrashLoopBackOff.

The old pods were already gone. The release went out the way a `Recreate`
strategy, or a crash that only starts after the pod passed its readiness check,
lets it: the old ReplicaSet scaled to zero before the new pods proved
themselves. With nothing left to fall back on, a crash loop is an outage.

## Diagnosis path

```bash
kubectl get pods -n {{.WorkloadNamespace}}                     # CrashLoopBackOff, RESTARTS climbing
kubectl get rs -n {{.WorkloadNamespace}}                       # old ReplicaSet at 0, new one never ready
kubectl logs -n {{.WorkloadNamespace}} <crashing-pod> --previous
# ... "listen tcp: address 80800: invalid port"   <- the app tells you why
kubectl describe pod -n {{.WorkloadNamespace}} <crashing-pod>  # Last State: Terminated, Exit Code 1
kubectl get deploy {{.WorkloadName}} -n {{.WorkloadNamespace}} \
  -o jsonpath='{.spec.template.spec.containers[0].env}'
# [{"name":"PORT","value":"80800"}, ...]          <- there's your problem
```

In Grafana, *Application Request Metrics* shows the impact (a red *No
available pods* region from the config change to the fix, *Responses by
outcome (k6 client)* turning from 200 to `no response`, k6's failed request
rate at 100%, and the app handling nothing while k6 keeps offering the same
load) and *Pod Resources* shows the
cause category (*Container restarts* climbing, *Containers not running, by
reason* reading CrashLoopBackOff).

## Fix

Put `PORT` back to the port the container declares ({{.WorkloadPort}}):

```bash
kubectl -n {{.WorkloadNamespace}} set env deploy/{{.WorkloadName}} PORT={{.WorkloadPort}}
kubectl -n {{.WorkloadNamespace}} rollout status deploy/{{.WorkloadName}}
```

`kubectl rollout undo deploy/{{.WorkloadName}} -n {{.WorkloadNamespace}}` works
too: it returns the template to the previous revision, config included.
Deleting the pod does not: the Deployment recreates it from the same broken
template.

Verify: `labctl incident status`. The detection check passes once every
replica is ready.

## Real-world parallel

A typo in a values file, an environment variable renamed in one place and not
the other, a setting that is valid in staging and not in production. The
lesson: CrashLoopBackOff is a symptom; the cause is in the previous
container's logs, and the fix belongs in the template the pods are built from.
When there are no logs at all, the app never started: read the spec (command,
args, image, env).
