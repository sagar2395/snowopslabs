# Evidence recipes

The commands each phase runs. Set the environment once:

```sh
FAULT=service-selector-broken
NS=go-api                                     # fault.yaml target.namespace
DOMAIN_SUFFIX=k3d.local
PROM=http://prometheus.$DOMAIN_SUFFIX
ALERTMANAGER_URL=http://alertmanager.$DOMAIN_SUFFIX
NOTES=$SCRATCHPAD/review-$FAULT.md            # every command and output lands here
```

## Before anything: is the lab healthy?

A review that starts on a sick cluster produces findings that belong to the
cluster, not the content. Take the baseline first, and keep it as the
comparison for I7's residue sweep:

```sh
kubectl get nodes
kubectl get --raw /readyz
kubectl get pods -A --field-selector=status.phase!=Running
kubectl get ns > "$SCRATCHPAD/ns.before.txt"
```

**When a command fails mid-review, prove the lab is healthy before recording a
finding.** An `inject.sh` that dies on
`failed to download openapi: ... EOF` is reporting a dead API server, not a
broken fault. `docker inspect k3d-<cluster>-server-0 --format '{{.RestartCount}}'`
climbing between two runs means the control plane is flapping — stop, say so,
and resume when it is stable. Re-run any failed step once on a confirmed-healthy
lab before it becomes a finding.

---

## I1 — static

```sh
./bin/labctl validate
./bin/labctl incident info "$FAULT"
make lint-shell

ls incidents/$FAULT/                          # fault.yaml inject.sh resolve.sh hints.md solution.md
grep -n 'expectAlert' incidents/$FAULT/fault.yaml
ls incidents/$FAULT/alerts/rule.yaml 2>/dev/null   # required iff expectAlert is set
grep -n "$FAULT" incidents/README.md               # listed in the fault table
```

## I2 — baseline: green when healthy

```sh
./bin/labctl incident inject "$FAULT"
./bin/labctl incident status
./bin/labctl incident resolve
./bin/labctl incident status                  # MUST pass on the healthy lab
```

## I3 — inject, idempotency, symptom

```sh
./bin/labctl incident inject "$FAULT"
./bin/labctl incident inject "$FAULT" --force # no-op, not an error
./bin/labctl incident status                  # MUST fail
```

`--force` is required for the idempotency test: without it the CLI refuses with
"an incident is already active" before `inject.sh` ever runs, so a plain second
`inject` tests the engine's guard rather than the script.

Compare what you observe to the `description` you wrote down in I0:

```sh
kubectl -n "$NS" get pods,svc,endpoints
curl -s -o /dev/null -w '%{http_code}\n' "http://go-api.$DOMAIN_SUFFIX/health"
```

A description promising "every pod Running and Ready" against pods in
`CrashLoopBackOff` is a defect — the learner is sent to the wrong layer.

## I4 — diagnose blind

`inject.sh`, `manifests/` and `solution.md` stay closed. Start the clock and the
load:

```sh
./bin/labctl traffic start --profile browse --rps 20

kubectl -n "$NS" get events --sort-by=.lastTimestamp | tail -20
kubectl -n "$NS" describe deploy <workload> | sed -n '/Events/,$p'
kubectl -n "$NS" logs deploy/<workload> --tail=50
kubectl top pods -n "$NS" 2>/dev/null
```

Then the on-call view — dashboards and the pager:

```sh
curl -s --get "$PROM/api/v1/query" \
  --data-urlencode 'query=sum by (code) (rate(http_requests_total[5m]))'
curl -s "$ALERTMANAGER_URL/api/v2/alerts" | jq -r '.[].labels.alertname' 2>/dev/null
```

Record the elapsed time and **which signal gave it away**. "Nothing did, I had
to `get` every object in the namespace" is the finding for dimension 2.

## I5 — hint ladder

```sh
./bin/labctl incident hint        # one at a time, read each fully
./bin/labctl incident solution    # must name a command that works verbatim
```

Fix by hand the way a learner would, then:

```sh
./bin/labctl incident status      # MUST pass on a genuine fix
```

## I6 — false-fix test

Both directions. Back up what you change, and restore between attempts.

**False green** — satisfy the check without fixing the fault. The check must
stay red:

```sh
# e.g. the check probes a URL: make something else answer on that path
kubectl -n "$NS" scale deploy <decoy> --replicas=1
./bin/labctl incident status      # MUST still fail
kubectl -n "$NS" scale deploy <decoy> --replicas=0
```

**False red** — fix the fault by a route the author did not imagine. The check
must go green:

```sh
kubectl -n "$NS" rollout restart deploy/<workload>
kubectl -n "$NS" rollout status deploy/<workload>
./bin/labctl incident status      # MUST pass
```

A false green caps the whole review at 3.5 — the grader does not grade. A false
red is scored almost as harshly: it teaches learners to distrust the grader.

Three things this test gets wrong if you improvise it:

- **Satisfy the whole request path, or the attempt proves nothing.** A decoy
  that carries only some of the Service's selector labels, or exposes a port the
  Service's named `targetPort` does not resolve to, never receives traffic — and
  the check staying red then looks like a check that held when it is really an
  attempt that missed. Read the full selector and the port names first:
  `kubectl -n "$NS" get svc <name> -o jsonpath='{.spec}' | jq '{selector,ports}'`.
- **A false green closes the run.** Once the detection check passes, the
  incident is recorded as resolved and `incident status` reports nothing active
  — while the fault is still live and the lab still broken. Recover with
  `./bin/labctl incident resolve <name>`, which is also I7's fourth state.
- **A zero-work pass is not always the grader's fault.** Before recording a
  false green, confirm the fault actually took hold — annotation present, the
  changed field changed, the symptom visible. A fault that never manifests
  produces the same passing check for an entirely different reason.

## I7 — escape hatch matrix

Run all four, resolving and re-injecting between them:

```sh
# 1. freshly injected, untouched
./bin/labctl incident inject "$FAULT" && ./bin/labctl incident resolve

# 2. half fixed by hand — the state that breaks naive resolve scripts
./bin/labctl incident inject "$FAULT"
kubectl -n "$NS" patch <partial fix>
./bin/labctl incident resolve

# 3. fully fixed by hand — must be a no-op, not an error
./bin/labctl incident inject "$FAULT"
kubectl -n "$NS" patch <full fix>
./bin/labctl incident resolve

# 4. active state lost — resolve by name
./bin/labctl incident inject "$FAULT"
rm -f .labctl/active-incident* 2>/dev/null
./bin/labctl incident resolve "$FAULT"
```

Then sweep for residue after each:

```sh
kubectl get ns | diff "$SCRATCHPAD/ns.before.txt" - || echo "NAMESPACE LEAK"
kubectl get all -A | grep -i labfault
kubectl -n "$NS" get deploy,svc -o json \
  | jq -r '.items[].metadata.annotations | keys[]?' | grep labfault
kubectl get prometheusrule -A | grep -i labfault    # armed alert must be disarmed
./bin/labctl incident history | tail -5             # MTTR and hints recorded
```

Any surviving `labfault-*` annotation, namespace or PrometheusRule is a
dimension-3 finding.

## Cleanup

```sh
./bin/labctl incident resolve || true
./bin/labctl incident status        # no incident active
./bin/labctl traffic stop
```

Leave the lab as you found it, and say in the report what you left running.
