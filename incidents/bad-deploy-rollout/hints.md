# Hints — bad-deploy-rollout

## Hint 1
The deploy "went out" but nothing changed for users. Check the state of the
rollout: `kubectl rollout status deploy/go-api -n go-api`. Is it actually
finished? Then look at the pods.

## Hint 2
A pod stuck in `ImagePullBackOff` or `ErrImagePull` can't even download its
container. `kubectl describe pod -n go-api <pod>` — the Events section
tells you exactly which image the kubelet tried to pull and why it failed.

## Hint 3
Compare the image on the Deployment
(`kubectl get deploy go-api -n go-api -o jsonpath='{.spec.template.spec.containers[0].image}'`)
with what's actually available. Fix the tag with `kubectl set image` (the
previously working image is recorded on the deployment's annotations), or
roll back with `kubectl rollout undo`.
