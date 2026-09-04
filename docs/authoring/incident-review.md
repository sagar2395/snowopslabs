# Incident review

The end-to-end quality gate for an incident. `labctl validate` proves a fault is
well-formed and `labctl incident resolve` proves it is reversible; this proves
it is **worth an on-call engineer's hour**.

It is the incident half of [scenario review](scenario-review.md), and it shares
that document's philosophy: seven phases against a live lab, evidence only, a
weighted rubric out of 5. An incident ships at **4.8 or above**, or at 4.7 when
the review has no remaining recommendation to make.

Run it with the `incident-review` skill, or by hand from this document.

---

## The principle being enforced

**The learner diagnoses; `labctl` breaks the lab and grades the fix.**

A scenario teaches you to build something. An incident teaches you to find
something — and the skill that transfers is the search, not the fix. The fix is
usually one `kubectl patch`; the value is in the twenty minutes before it, spent
reading endpoints, events, dashboards and logs.

That inverts what the review looks for. A scenario is judged on whether the
learner performs real work; an incident is judged on whether the learner can
*find* the fault from the symptom alone, and on whether the detection check is
honest about when they have actually fixed it.

Four corollaries the review tests directly:

1. **The detection check is honest in both directions.** Green on a healthy lab,
   red the moment the fault is live, and green again only for a genuine fix.
   Two of those three are routinely untested.
2. **The symptom is discoverable through observability** — a dashboard, an
   alert, an event, a log line — without ever opening `inject.sh`. A fault you
   can only find by reading its own source is a puzzle, not a drill.
3. **The hint ladder adds exactly one rung at a time**, from "where would you
   look" to "here is the resource", with the command reserved for the solution.
4. **The escape hatch never fails**, from any state the learner can leave the
   lab in — including half-fixed, which is the state that breaks naive
   `resolve.sh` scripts.

---

## The seven phases

Run them in order. Phase I4 has a rule the others do not: **do not read
`inject.sh` or `solution.md` until I4 is over.** Once you know the answer you
can no longer assess whether the fault was findable, and that is 0.20 of the
score.

### I0 — Read the symptom, not the fault

Read `fault.yaml`'s `description`, `displayName`, `severity` and `target`, and
its entry in the fault table in
[incidents/README.md](../../incidents/README.md). Nothing else. Write down the
symptom the incident promises the victim will experience — "503s through the
ingress while every pod is Ready" — because I3 tests whether that is what
actually happens, and I4 tests whether it is enough to go on.

### I1 — Static validation

```bash
labctl validate
labctl incident info <name>
make lint-shell
```

Confirm: `fault.yaml`, `inject.sh`, `resolve.sh`, `hints.md` and `solution.md`
are all present; `alerts/rule.yaml` exists if and only if `expectAlert` is set;
the target is a demo app or a dedicated `labfault-*` namespace and never a
platform component; the fault is listed in the README table; `snippets[].path`
and `references[].url` resolve.

### I2 — Baseline: green when healthy

The phase everyone skips, and the one that catches a check asserting the wrong
thing entirely.

```bash
labctl incident inject <name>
labctl incident status        # note the result
labctl incident resolve
labctl incident status        # must now PASS
```

A detection check that cannot go green on a healthy lab will grade every learner
as broken forever. A check that is green for a reason unrelated to the fault —
because it probes a URL that answers regardless — will grade every learner as
finished immediately.

### I3 — Inject: idempotency, and does the symptom match?

```bash
labctl incident inject <name>
labctl incident inject <name>     # second run: a no-op, not an error
labctl incident status            # must FAIL
```

Then compare reality to the `description` you wrote down in I0. If the promise
is "pods are Running and Ready but the service is dark" and the pods are in fact
`CrashLoopBackOff`, the description is a defect: the learner is hunting the
wrong layer.

### I4 — Diagnose it blind

Time-boxed, with `inject.sh` and `solution.md` closed. Work only from the
symptom and the observability the lab provides:

```bash
labctl traffic start --profile browse --rps 20     # make the impact visible
kubectl -n <ns> get pods,svc,endpoints
kubectl -n <ns> get events --sort-by=.lastTimestamp | tail -20
kubectl -n <ns> logs deploy/<workload> --tail=50
```

Then look at it the way an on-call engineer would — the Grafana dashboards, and
the pager if the fault declares `expectAlert`. Record how long it took, and
which signal actually gave it away. **If nothing in the lab's observability
points at the fault, that is the finding**, however elegant the fault is.

### I5 — Walk the hint ladder

Reset your knowledge as far as you honestly can, and consume the hints one at a
time:

```bash
labctl incident hint      # repeat, reading each fully before taking the next
labctl incident solution
```

Each hint must add exactly one rung. A hint that names the resource in position
one collapses the drill; a hint that repeats the previous one wastes it. The
solution must name the command, and the command must work.

Then fix it by hand, the way a learner would, and confirm the check turns:

```bash
labctl incident status    # must PASS on a genuine fix
```

### I6 — The false-fix test

The incident's answer to the scenario tamper test, run in both directions.

**False green** — fix the check without fixing the fault. Make the narrowest
change that satisfies the check literally while the fault is still live: point
the probed URL at something healthy, scale a second workload in to answer, patch
the field the check reads without repairing the cause. The check **must stay
red**.

**False red** — fix the fault by a legitimate route the author did not imagine.
Restart the deployment instead of patching, re-apply the app's Helm release,
`kubectl delete pod` and let it come back correct. The check **must go green**;
a check that only recognises one spelling of the fix teaches learners to distrust
the grader.

| Fault shape | False green to try | False red to try |
|---|---|---|
| Service selector mismatch | label a decoy pod to match the broken selector | re-apply the Service from the app's chart |
| CrashLoop from a bad command | scale to zero so nothing is crashing | delete the deployment and redeploy |
| Blackhole NetworkPolicy | add a second, permissive policy alongside it | delete the namespace's policies wholesale |
| OOMKill from a low limit | raise the limit far past what is sane | fix it via a Helm value rather than a patch |

### I7 — The escape hatch matrix

`resolve.sh` runs after any amount of manual fixing, so test it from every state
the learner can leave behind:

| Start state | `labctl incident resolve` must |
|---|---|
| freshly injected, untouched | restore cleanly |
| half fixed by hand | restore cleanly, not fight the manual change |
| fully fixed by hand | be a no-op, not an error |
| active state lost | work when named: `labctl incident resolve <name>` |

After each, sweep for residue:

```bash
kubectl get ns
kubectl get all -A | grep -i labfault
kubectl -n <ns> get deploy <workload> -o jsonpath='{.metadata.annotations}'
kubectl get prometheusrule -A | grep -i labfault      # armed alert disarmed?
labctl incident history                               # MTTR and hints recorded
```

Annotations named `labfault-*` are how `resolve.sh` remembers the original
values; none may survive a resolve.

---

## The rubric

Eight dimensions, each scored 0–5, combined by weight.

| # | Dimension | Weight | What a 5 looks like |
|---|---|---|---|
| 1 | **Detection integrity** | 0.20 | Green healthy, red injected, green only on a genuine fix; survives both halves of I6 |
| 2 | **Diagnosability** | 0.20 | Findable from the symptom and the lab's own observability, with `inject.sh` closed |
| 3 | **Reversibility & safety** | 0.15 | Resolves from all four states; `inject` idempotent; blast radius confined to demo apps; no `labfault-*` residue |
| 4 | **Hint ladder & solution** | 0.15 | Each hint adds one rung; the solution names a command that works |
| 5 | **Production realism** | 0.10 | A failure that really happens, presenting the way it really presents |
| 6 | **Observability & paging** | 0.10 | The symptom shows on a dashboard or log; an `expectAlert` fault actually pages and disarms on resolve |
| 7 | **Invariant compliance** | 0.05 | POSIX shell, `make lint-shell` clean, changes recorded via annotations, README table current |
| 8 | **Self-sufficiency** | 0.05 | Diagnosable and fixable from `info`, hints, references and snippets alone |

**Anchors.** 5 — no finding. 4 — one cosmetic finding. 3 — a finding a learner
would notice. 2 — a finding that misleads a learner. 1 — the dimension's promise
is not delivered. 0 — the incident teaches the wrong lesson, or leaves the lab
damaged.

Score to two decimals. Every dimension below 5 carries a named finding, and a
score rises only on new evidence from a re-run.

## The loop

Identical to the scenario loop: fix the highest-impact finding, re-run the
phases it touches, re-score, and stop at 4.8 — or at 4.7 with nothing left to
recommend. A review that cannot converge in four rounds is reported as failed
with its open findings named.

Two incident-specific cautions:

- **I4 is single-use per reviewer.** Once you know the answer, a re-run cannot
  re-score diagnosability honestly. If a fix changes the fault's symptom, say
  in the report that I4 was scored from the first run only.
- **A fix to `resolve.sh` re-runs the whole of I7**, not just the state you
  changed. The states interact.

## Related

- [incidents/README.md](../../incidents/README.md) — the fault contract
- [Scenario review](scenario-review.md) — the build-loop half of this gate
- [Learning review](learning-review.md) — the gate for the challenges that wrap
  this fault; a challenge cannot score above the incident underneath it
- [CLI: incidents](../reference/cli/incidents.md) — commands, timing, history
- [R13](../runbooks/R13-observability-pipeline.md) — why a symptom is invisible
