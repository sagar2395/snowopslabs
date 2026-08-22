# Solution — bad-deploy-rollout

## What happened

The go-api Deployment was updated to an image tag that doesn't exist
(`registry.invalid/never-pushed/go-api:v99.9.9`). The kubelet can't pull
it, so the new ReplicaSet's pods loop in ImagePullBackOff and the rollout
never completes. Old pods keep serving — a quiet failure unless you watch
rollout health.

## Diagnosis path

```bash
kubectl rollout status deploy/go-api -n go-api    # stuck
kubectl get pods -n go-api                        # ImagePullBackOff on new pods
kubectl describe pod -n go-api <pod>              # Events: "Failed to pull image ... not found"
kubectl get deploy go-api -n go-api -o jsonpath='{.spec.template.spec.containers[0].image}'
```

## Fix

Either roll back:

```bash
kubectl -n go-api rollout undo deploy/go-api
```

or set the image back explicitly (the original is recorded in the
`labfault-bad-deploy-rollout-original` annotation):

```bash
kubectl -n go-api get deploy go-api -o jsonpath='{.metadata.annotations.labfault-bad-deploy-rollout-original}'
kubectl -n go-api set image deploy/go-api go-api=<that-image>
kubectl -n go-api rollout status deploy/go-api
```

## Real-world parallel

The most common deploy failure there is: CI tagged the image differently
than the manifest expected, the push step failed silently, or someone
fat-fingered a tag. Guard rails: `kubectl rollout status` as a pipeline
gate, image-existence checks pre-deploy, and alerts on
`kube_deployment_status_condition{condition="Progressing",status="false"}`.
