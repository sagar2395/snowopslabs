# Learning review

The end-to-end quality gate for a learning path or a challenge. `labctl
validate` proves the YAML is well-formed and its refs resolve; this proves the
path **teaches** and the challenge **grades**.

It is the third harness alongside [scenario review](scenario-review.md) and
[incident review](incident-review.md), and it shares their philosophy: phases
against a live lab, evidence only, a weighted rubric out of 5. Content ships at
**4.8 or above**, or at 4.7 when the review has no remaining recommendation.

Paths and challenges are reviewed differently — a path is a curriculum, a
challenge is an exam — so this document carries two phase lists and two rubrics.
Run it with the `learning-review` skill.

---

## The principle being enforced

**A path and a challenge are thin wrappers over content that already exists.
Neither may introduce mechanics of its own, and neither is better than what it
wraps.**

That has a consequence the review acts on directly: a challenge over an incident
inherits that incident's diagnosability, and a path module that runs a scenario
inherits that scenario's grading integrity. **Review the underlying content
first.** A challenge cannot score above the incident it wraps, because every
minute the learner spends is spent inside that fault.

What the wrapper adds is its own to get right:

- A path adds **sequence and explanation** — the order modules come in, and the
  intro that says why this module exists. Both are the path's own work.
- A challenge adds **a clock and a score**. Both are the challenge's own work,
  and both are easy to get wrong in ways that make every grade meaningless.

---

## Reviewing a path — phases L0–L7

### L0 — Read the promise

Read `path.yaml`'s `description`, `tags`, `estimatedMinutes` and the module
titles, plus the entry in [learn/README.md](../../learn/README.md). Write down
the arc it promises — "by the end you will have used kubectl, Helm, Prometheus
and the incident engine" — and the time it claims. L3 tests the time; L7 tests
the arc.

### L1 — Static validation

```bash
labctl validate
labctl learn list
```

Confirm: every `action.ref` names a scenario, incident or command that exists;
every `intro` and `script` path resolves and stays inside the path directory;
module names are unique; the path is listed in the README.

### L2 — The premature-green sweep

**The headline phase, and the defect this content class fails on most often.**

Put the lab in the state each module *starts* from, and run that module's check
**before doing the module's work**. Every check must fail.

```bash
labctl learn start <path>
labctl learn next <path>        # must NOT pass before you do anything
```

The failure mode is a check that asserts something the platform already
provides. A module whose action is "activate the observability scenario" and
whose check is `GET http://prometheus/-/ready` is green before the learner lifts
a finger, because Prometheus is part of the platform. The check has to assert
what *this module's work* produced — the dashboard the scenario installs, the
metric it starts collecting — not that the lab is up.

Work through every module this way. A path where any check is green early does
not teach; it advances.

### L3 — The cold walk

On a lab in the path's declared starting state, do the whole path in order, using
**only the intro and the action ref**. Time each module.

```bash
labctl learn start <path>
labctl learn next <path>        # after each module's work
labctl learn progress <path>
```

Record every moment you had to look outside the path to proceed, and compare the
total elapsed time to `estimatedMinutes`. A path claiming 45 minutes that takes
two hours has misled someone into starting it on a lunch break.

### L4 — State handoff

Modules run in declaration order, and each inherits whatever the last one left.
Confirm that inheritance is real:

- Module N's action must be possible in the state module N−1 ends in.
- `labctl learn next` must refuse to skip ahead.
- A module that tears something down must not strand a later module that needs
  it.

### L5 — Teaching quality

Read every intro as someone who has not done this before. An intro earns its
place by explaining **why this module exists and what to look at** — not by
restating the command that is already in `action.ref`. A module with no intro,
or an intro that is a paraphrase of its command, leaves the learner pasting
commands they cannot explain afterwards.

Check the ramp too: difficulty should climb. A path whose last module is as easy
as its first has not taken the learner anywhere.

### L6 — Resumability

Learners do not finish in one sitting.

```bash
labctl learn progress <path>    # accurate mid-path
# ...restart the shell, and come back...
labctl learn progress <path>    # progress survived
labctl learn next <path>        # re-running a completed module is safe
```

### L7 — The arc

Re-read what you wrote in L0. Does someone who finished this path have the skill
the description promised? A path that installs four things and verifies four
things, without ever asking the learner to interpret anything, has toured the
lab rather than taught it.

---

## Reviewing a challenge — phases C0–C6

### C0 — Read the promise, and the content underneath

Read `challenge.yaml` and the README table row. Then establish the quality of
what it wraps: has the incident or scenario in `setup.ref` passed its own
review? If not, run that review first — a challenge over a fault nobody can
diagnose is an unfair exam, and no amount of grading polish fixes it.

### C1 — Static validation

```bash
labctl validate
labctl challenge info <name>
```

Confirm: `setup.ref` resolves; `grading.useDetectionCheck: true` is used for an
incident challenge unless there is a stated reason not to — hand-written checks
drift from the fault's own detection check, and then `challenge submit` and
`incident status` disagree; `parTime` is present and is a Go duration;
`hintPenalty` is set or deliberately defaulted; the README table row matches the
YAML.

### C2 — The zero-work submit

The challenge's answer to the tamper test. Start it and submit immediately,
having fixed nothing:

```bash
labctl challenge start <name>
labctl challenge submit         # score MUST be at or near zero
labctl challenge abort
```

A challenge that awards a passing score for doing nothing is not an assessment.
This is scored as harshly here as a false-green detection check is in an
incident review.

### C3 — The honest timed run

Start the clock and actually do it — diagnose, fix, submit:

```bash
labctl challenge start <name>
# ...diagnose and fix, taking no hints, timing yourself...
labctl challenge submit
```

Two outputs matter: the score (which must be high for a genuine fix) and **your
elapsed time, which is the evidence for C4**. Record both.

### C4 — Par-time calibration

`parTime` drives the time deduction, so a wrong par makes every score wrong.
Compare the declared par to your C3 run:

- Par far below a competent run punishes everyone, including the author.
- Par far above it means the clock never bites and speed is not rewarded.
- Par should be reachable **with one or two lookups** — the README's own rule.

A `parTime` that cannot be traced to a real timed run is a finding, whatever the
number is.

### C5 — Score arithmetic

Confirm the documented formula is what actually happens:

```
final = (100 − hints×penalty − min(20, (elapsed−par)/par × 20)) × checks_passed/checks_total
```

Take a hint and confirm the deduction lands at `hintPenalty`. Run past par and
confirm the time deduction appears and **caps at 20**. If grading uses explicit
checks rather than the detection check, confirm a partial fix scales the score
rather than zeroing or maxing it.

### C6 — Abort and history

```bash
labctl challenge start <name>
labctl challenge abort          # must undo the setup cleanly, no score
labctl challenge history        # the run is recorded
```

Then sweep the lab the way an incident review does — `abort` runs the same
teardown as `incident resolve`, so the same residue matters: no `labfault-*`
annotations, no orphaned namespace, no armed PrometheusRule.

---

## The rubrics

### Path

| # | Dimension | Weight | What a 5 looks like |
|---|---|---|---|
| 1 | **Check integrity** | 0.20 | Every module's check fails before the work and passes after; none is green from the platform alone |
| 2 | **Teaching quality** | 0.20 | Every intro explains why the module exists and what to look at; difficulty ramps |
| 3 | **State coherence** | 0.15 | Each module runs in the state the last one left; `learn next` will not skip |
| 4 | **Arc & outcome** | 0.15 | Finishing delivers the skill the description promised |
| 5 | **Time honesty** | 0.10 | `estimatedMinutes` matches a real cold walk |
| 6 | **Resumability** | 0.10 | Progress survives a restart; completed modules are safe to re-run |
| 7 | **Compliance & self-sufficiency** | 0.10 | Refs resolve, assets stay in the directory, no external credentials; finishable without leaving the path |

### Challenge

| # | Dimension | Weight | What a 5 looks like |
|---|---|---|---|
| 1 | **Grading integrity** | 0.25 | Zero-work submit scores ~0, a genuine fix scores high, and the arithmetic matches the published formula |
| 2 | **Par-time calibration** | 0.20 | `parTime` traced to a real timed run, reachable with one or two lookups |
| 3 | **Underlying content** | 0.15 | The wrapped incident or scenario is itself review-clean |
| 4 | **Completability** | 0.15 | Finishable on a fresh lab, start to submit, without outside help |
| 5 | **Abort & cleanliness** | 0.10 | `abort` undoes the setup from any state, leaving no residue |
| 6 | **Framing** | 0.10 | The description sets the task and the stakes without giving away the answer |
| 7 | **Invariant compliance** | 0.10 | `useDetectionCheck` preferred, README table current, one active challenge honoured |

**Anchors** for both. 5 — no finding. 4 — one cosmetic finding. 3 — a finding a
learner would notice. 2 — a finding that misleads a learner. 1 — the dimension's
promise is not delivered. 0 — the content teaches or grades the wrong thing.

Score to two decimals. Every dimension below 5 carries a named finding, and a
score rises only on new evidence from a re-run.

**Two caps.** A path with any premature-green check cannot score above 3.5 — it
advances learners without teaching them. A challenge whose zero-work submit
passes cannot score above 3.5 — it grades nothing.

## The loop

Fix the highest-impact finding, re-run the phases it touches, re-score, and stop
at 4.8 — or at 4.7 with nothing left to recommend. Four rounds without
convergence is a failed review, reported with its open findings named.

One caution specific to this content class: **a fix to the wrapped content
re-runs the wrapper's review.** Retuning an incident's detection check changes
what `challenge submit` grades and what a path module's incident step proves.

## Related

- [learn/README.md](../../learn/README.md) — the path contract
- [challenges/README.md](../../challenges/README.md) — the challenge contract and score formula
- [Scenario review](scenario-review.md) · [Incident review](incident-review.md) — the content underneath
- [CLI: learning & challenges](../reference/cli/learning.md)
