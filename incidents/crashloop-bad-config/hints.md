# Hints — crashloop-bad-config

## Hint 1
Something changed about the go-api deployment recently. Start where you
always should: `kubectl get pods -n go-api` and look at the STATUS and
RESTARTS columns. What's different between the old and new pods?

## Hint 2
A pod that dies instantly usually logs why — or dies too fast to log
anything, which is itself a clue. Compare
`kubectl logs -n go-api <crashing-pod>` (and `--previous`) with
`kubectl describe pod` — check the container's Last State and exit code.

## Hint 3
Exit code 1 with no app logs means the app never started. Inspect the pod
*spec*, not its status: `kubectl get deploy go-api -n go-api -o yaml` and
look at the container's `command`. Does that look like it belongs there?
Remove the override and watch the rollout recover.
