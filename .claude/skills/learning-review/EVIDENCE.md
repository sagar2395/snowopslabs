# Evidence recipes

The commands each phase runs. Set the environment once:

```sh
PATH_NAME=kubernetes-foundations
CHALLENGE=make-the-slo-green
DOMAIN_SUFFIX=k3d.local
NOTES=$SCRATCHPAD/review-$PATH_NAME.md        # every command and output lands here
```

## Before anything: is the lab healthy?

A review that starts on a sick cluster produces findings that belong to the
cluster, not the content:

```sh
kubectl get nodes
kubectl get --raw /readyz
kubectl get ns > "$SCRATCHPAD/ns.before.txt"
./bin/labctl challenge status                 # nothing active before you begin
./bin/labctl incident status
```

**When a command fails mid-review, prove the lab is healthy before recording a
finding.** A `docker inspect k3d-<cluster>-server-0 --format '{{.RestartCount}}'`
climbing between two runs means the control plane is flapping — stop, say so,
and resume when it is stable.

---

# Path track

## L1 — static

```sh
./bin/labctl validate
./bin/labctl learn list
./bin/labctl learn progress "$PATH_NAME"

grep -n 'ref:' learn/$PATH_NAME/path.yaml     # every ref must name real content
ls learn/$PATH_NAME/intros/ learn/$PATH_NAME/checks/
grep -n "$PATH_NAME" learn/README.md
```

## L2 — the premature-green sweep

`--show-only` prints a module's intro **without running its check**, which is
how you read ahead without consuming the module:

```sh
./bin/labctl learn start "$PATH_NAME"         # start (or restart) the path
./bin/labctl learn next "$PATH_NAME" --show-only   # read the module
./bin/labctl learn next "$PATH_NAME"          # MUST NOT pass before you do the work
```

Repeat for every module: do the previous modules' work, then test the next
module's check **before** doing its work. A check that is already green is the
finding — name the module, the check, and what it is actually asserting.

The tell is a check that asserts something the platform provides rather than
something the module produced:

```sh
# green before the learner activates anything, because the platform runs Prometheus
curl -s -o /dev/null -w '%{http_code}\n' "http://prometheus.$DOMAIN_SUFFIX/-/ready"
```

The replacement asserts the module's own output — the dashboard the scenario
installs, the metric it starts collecting, the workload it deploys. Read the
scenario's `prerequisites:` block to tell the two apart: anything listed there
is a precondition of the module's action and is green before the learner starts.

**Avoid checks that grep CLI output.** They break on wording and on the working
directory — a check that greps `labctl incident status` for `no incident`
silently never matches `No incident is active`, and one that calls `bin/labctl`
by a relative path fails whenever labctl was not started from the project root.
Assert cluster state, or the run history, and derive paths from the script's own
location:

```sh
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
```

Prove a replacement check in **both** directions before trusting it — create the
thing it asserts and confirm it goes green, then remove it and confirm it goes
red again. A fixture is enough where cluster state is expensive to arrange.

## L3 — the cold walk

Reset progress and walk it in order, timing each module:

```sh
./bin/labctl learn start "$PATH_NAME"         # restart from module 1
date                                          # per-module start
./bin/labctl learn next "$PATH_NAME" --show-only
# ...do the module using ONLY the intro and the action ref...
./bin/labctl learn next "$PATH_NAME"
date                                          # per-module end
```

Record each module's elapsed time and every moment you looked outside the path
to proceed. Total vs `estimatedMinutes` is dimension 5.

## L4 — state handoff

```sh
./bin/labctl learn start "$PATH_NAME"
./bin/labctl learn next "$PATH_NAME"          # module 1's check, work undone: must fail
# skipping is not offered — confirm `next` walks in declaration order only
./bin/labctl learn progress "$PATH_NAME"
```

For each module, ask whether its action is possible in the state its predecessor
left. A module that only works because of something on your lab that the path
never established fails on a fresh one.

## L6 — resumability

```sh
./bin/labctl learn progress "$PATH_NAME"      # mid-path: accurate?
ls .labctl/learn/                             # progress is stored here
# start a new shell, then:
./bin/labctl learn progress "$PATH_NAME"      # survived?
./bin/labctl learn next "$PATH_NAME"          # re-running a completed module is safe
```

---

# Challenge track

## C0 — the content underneath

```sh
./bin/labctl challenge info "$CHALLENGE"
grep -n -A2 'setup:' challenges/$CHALLENGE/challenge.yaml
```

Then establish whether that incident or scenario has passed its own review. Name
the review you relied on in the report; if there is none, say so and score
dimension 3 as unreviewed rather than guessing.

## C1 — static

```sh
./bin/labctl validate
grep -n -E 'parTime|useDetectionCheck|hintPenalty' challenges/$CHALLENGE/challenge.yaml
grep -n "$CHALLENGE" challenges/README.md     # table row matches the YAML
```

## C2 — the zero-work submit

```sh
./bin/labctl challenge start "$CHALLENGE"
./bin/labctl challenge submit                 # score MUST be at or near zero
./bin/labctl challenge abort
```

A passing score here caps the whole review at 3.5 — but **read the check line,
not just the score**, in both directions:

- **A 0 can be a lie.** A check that *errored* rather than failed also scores 0.
  An unresolved template (`invalid character "{" in host name`) or a missing
  script (`exit status 127: no such file or directory`) means the grader never
  ran the check, and a genuine fix will score 0 too — the challenge is
  uncompletable, which is a dimension-4 finding, not a passing C2.
- **A 100 can be the fault's fault.** If a zero-work submit scores 100, confirm
  the setup actually took hold before blaming the grader: the `labfault-*`
  annotation is present, the field it changes has changed, and the symptom is
  visible. A fault that never manifests grades as "already fixed".

```sh
kubectl -n "$NS" get deploy <workload> -o jsonpath='{.metadata.annotations}' | tr ',' '\n' | grep labfault
```

`challenge submit` closes the run even when it fails, but leaves the underlying
fault injected — so the next `challenge start` is refused with "an incident is
already active". Resolve between attempts:

```sh
./bin/labctl incident resolve
```

## C3 — the honest timed run

No hints, and time yourself properly:

```sh
date
./bin/labctl challenge start "$CHALLENGE"
# ...diagnose and fix it the way a learner would...
./bin/labctl challenge status                 # elapsed, hints used
./bin/labctl challenge submit
date
```

Record the score **and** your elapsed time. The elapsed time is the only
admissible evidence for C4.

## C5 — score arithmetic

Verify each term of the published formula separately:

```sh
# hint deduction lands at hintPenalty
./bin/labctl challenge start "$CHALLENGE"
./bin/labctl challenge hint
./bin/labctl challenge status
# ...fix it, then submit and compare to the no-hint run from C3...
./bin/labctl challenge submit

# time deduction appears past par and caps at 20
./bin/labctl challenge history | tail -5
```

If grading uses explicit `checks:` rather than `useDetectionCheck`, confirm a
partial fix scales the score rather than zeroing or maxing it.

## C6 — abort and residue

```sh
./bin/labctl challenge start "$CHALLENGE"
./bin/labctl challenge abort
./bin/labctl challenge history | tail -3      # the run is recorded

kubectl get ns | diff "$SCRATCHPAD/ns.before.txt" - || echo "NAMESPACE LEAK"
kubectl get all -A | grep -i labfault
kubectl get prometheusrule -A | grep -i labfault
./bin/labctl incident status                  # the setup fault is gone
```

---

## Cleanup

```sh
./bin/labctl challenge abort || true
./bin/labctl incident resolve || true
./bin/labctl traffic stop || true
./bin/labctl learn progress                   # note what you consumed
```

A review consumes path progress. Say in the report which paths you reset, and
leave the lab as you found it otherwise.
