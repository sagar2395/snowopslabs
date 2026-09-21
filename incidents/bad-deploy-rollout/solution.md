# Solution — bad-deploy-rollout

## What happened

The {{.WorkloadName}} Deployment was updated to an image tag that doesn't exist
(`registry.invalid/never-pushed/{{.WorkloadName}}:v99.9.9`). The kubelet can't pull
it, so the new ReplicaSet's pods loop in ImagePullBackOff and the rollout
never completes. Old pods keep serving — a quiet failure unless you watch
rollout health.

## Diagnosis path

```bash
kubectl rollout status deploy/{{.WorkloadName}} -n {{.WorkloadNamespace}}    # stuck
kubectl get pods -n {{.WorkloadNamespace}}                        # ImagePullBackOff on new pods
kubectl describe pod -n {{.WorkloadNamespace}} <pod>              # Events: "Failed to pull image ... not found"
kubectl get deploy {{.WorkloadName}} -n {{.WorkloadNamespace}} -o jsonpath='{.spec.template.spec.containers[0].image}'
```

## Fix

Either roll back:

```bash
kubectl -n {{.WorkloadNamespace}} rollout undo deploy/{{.WorkloadName}}
```

or set the image back explicitly (the original is recorded in the
`labfault-bad-deploy-rollout-original` annotation):

```bash
kubectl -n {{.WorkloadNamespace}} get deploy {{.WorkloadName}} -o jsonpath='{.metadata.annotations.labfault-bad-deploy-rollout-original}'
kubectl -n {{.WorkloadNamespace}} set image deploy/{{.WorkloadName}} {{.WorkloadName}}=<that-image>
kubectl -n {{.WorkloadNamespace}} rollout status deploy/{{.WorkloadName}}
```

## Real-world parallel

The most common deploy failure there is: CI tagged the image differently
than the manifest expected, the push step failed silently, or someone
fat-fingered a tag. Guard rails: `kubectl rollout status` as a pipeline
gate, image-existence checks pre-deploy, and alerts on
`kube_deployment_status_condition{condition="Progressing",status="false"}`.
