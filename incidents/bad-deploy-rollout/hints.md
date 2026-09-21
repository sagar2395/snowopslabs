# Hints — bad-deploy-rollout

## Hint 1
The deploy "went out" but nothing changed for users. Check the state of the
rollout: `kubectl rollout status deploy/{{.WorkloadName}} -n {{.WorkloadNamespace}}`. Is it actually
finished? Then look at the pods.

## Hint 2
A pod stuck in `ImagePullBackOff` or `ErrImagePull` can't even download its
container. `kubectl describe pod -n {{.WorkloadNamespace}} <pod>` — the Events section
tells you exactly which image the kubelet tried to pull and why it failed.

## Hint 3
Compare the image on the Deployment
(`kubectl get deploy {{.WorkloadName}} -n {{.WorkloadNamespace}} -o jsonpath='{.spec.template.spec.containers[0].image}'`)
with the one the pod that is still serving is running. The Deployment's own
history remembers the last good release — `kubectl rollout history deploy/{{.WorkloadName}}
-n {{.WorkloadNamespace}} --revision=<n>` shows the image each revision used.

Put the working tag back with `kubectl set image`, or roll the release back
with `kubectl rollout undo`.
