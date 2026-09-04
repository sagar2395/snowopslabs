---
name: learning-author
description: "Write, edit, debug or review a SnowOps Labs learning path or challenge — path.yaml, modules, intros, completion checks, challenge.yaml, par times and grading. Use whenever a task involves anything under learn/ or challenges/, or `labctl learn|challenge`."
user-invocable: true
---

# Authoring learning paths and challenges

Both are thin wrappers over content that already exists. A path sequences
scenarios, incidents and commands into modules; a challenge puts a clock and a
grade on one of them. Neither should introduce new mechanics of its own.

## Read first

- **Paths:** [learn/README.md](../../../learn/README.md) — `path.yaml`, module
  shape, `action.type` and `check.type`.
- **Challenges:** [challenges/README.md](../../../challenges/README.md) —
  `challenge.yaml` and the score formula.
- Commands for both:
  [docs/reference/cli/learning.md](../../../docs/reference/cli/learning.md).

Checks use the same types as scenario checks — see
[the scenario schema](../../../docs/reference/scenario-schema.md#checks).

## What makes a path or challenge worth doing

Both are wrappers, and **neither is better than what it wraps**. A challenge
over an incident inherits that incident's diagnosability; a path module running
a scenario inherits that scenario's grading integrity. Review the underlying
content first — polishing a clock and a score over a fault nobody can diagnose
does not make a fair exam.

What the wrapper owns is its own to get right:

**A path owns sequence and explanation.** The order modules come in, and the
intro that says why this module exists and what to look at. An intro that
paraphrases the command already in `action.ref` has added nothing; the learner
finishes able to repeat commands and unable to explain them.

**A challenge owns the clock and the score.** Both are easy to get wrong in ways
that make every grade meaningless — a `parTime` guessed rather than measured, a
submit that pays out for doing nothing.

**The check must be provable by the learner's own work.** This is where this
content class fails most often: a check that is green from the platform alone,
before the learner lifts a finger. A module that activates the observability
scenario and checks `GET http://prometheus/-/ready` passes on a lab that already
runs Prometheus — which is every lab. Assert what *this module produced*: the
dashboard the scenario installs, the metric it starts collecting.

**A score must be defensible in both directions.** Submitting without fixing
anything scores nothing; a genuine fix scores high; hints and time deduct by the
published formula.

## Learning paths

A module is one thing the learner does, plus one check proving they did it.

```yaml
modules:
  - name: init-cluster                    # unique within the path
    displayName: "Start the cluster"
    intro: intros/01-init-cluster.md      # optional, but write one
    action:
      type: command                       # command | scenario | incident
      ref: "labctl runtime up"
    check:
      name: cluster-reachable
      type: script                        # http | script | promql
      script: checks/cluster-reachable.sh
      timeoutSeconds: 30
```

- `action.type: command` shows the `ref` verbatim for the learner to run.
- `action.type: scenario` or `incident` points at content by name, which
  `labctl validate` cross-checks — a dangling reference is an error.
- The check must be **provable by the learner's own work**, not by the module
  having been displayed. A check that passes before the learner does anything is
  the most common defect here.

Order modules so each one leaves the lab in the state the next one assumes.
`labctl learn next` walks them in declaration order and will not skip ahead.

## Challenges

```yaml
name: restore-broken-deploy
parTime: "10m"                  # Go duration; omit for unscored time
setup:
  type: incident                # incident | scenario
  ref: bad-deploy-rollout
grading:
  useDetectionCheck: true       # reuse the fault's own detection check
hintPenalty: 5                  # % per hint (default 5)
```

Prefer `useDetectionCheck: true`. The fault's detection check is already
written as "what must be true when healthy", which is exactly the grading
question — a second, hand-written set of checks drifts from it.

Set `parTime` from an actual timed run, not a guess — do the challenge yourself
with a clock and no hints, and use that number. It drives the time deduction, so
a wrong par time makes every score wrong. A par a correct solution cannot meet
punishes everyone; a par nobody can exceed means the clock never bites:

```
final = (100 − hints×penalty − min(20, (elapsed−par)/par × 20)) × checks_passed/checks_total
```

## Verify your work

```bash
labctl validate                          # schema and cross-references
labctl learn start my-path
labctl learn next my-path                # must NOT pass before you do the work
# ...do the module...
labctl learn next my-path                # must pass and advance
labctl learn progress my-path
```

```bash
labctl challenge start my-challenge
labctl challenge submit                  # having fixed NOTHING: must score ~0
labctl challenge abort
labctl challenge start my-challenge
# ...actually fix it, timed, with no hints...
labctl challenge submit                  # must score high; note your elapsed time
labctl challenge abort                   # must undo the setup cleanly
```

## Then review it

Valid YAML and resolving refs do not make a path that teaches or a challenge
that grades. Hand every new or materially changed one to the
[learning-review](../learning-review/SKILL.md) skill, which walks the path cold
on a live lab, sweeps for checks that are green before the learner works, times
a real challenge run to calibrate par, confirms a zero-work submit scores
nothing, scores it out of 5 and improves it until it reaches 4.8.

```
/learning-review my-path
```

Treat the score as part of the definition of done: below 4.8 it is a draft,
whatever `validate` says. Two findings cap a review at 3.5 on their own — a path
with any premature-green check, and a challenge whose zero-work submit passes.

## Before you finish

- [ ] `labctl validate` is clean; every `ref` resolves.
- [ ] Every check fails before the work and passes after — tested by running it
      *before* doing the module, not just after.
- [ ] No check is green from the platform alone.
- [ ] Every intro explains why the module exists, not just what to type.
- [ ] A zero-work `challenge submit` scores ~0.
- [ ] Modules leave the lab in the state the next module assumes.
- [ ] `parTime` came from a real timed run — record the figure.
- [ ] `learning-review` scores it 4.8 or better.
- [ ] Listed in the table in the relevant README.
- [ ] `make docs-check` passes.
