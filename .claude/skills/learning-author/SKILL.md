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

Set `parTime` from an actual timed run, not a guess. It drives the time
deduction, so a wrong par time makes every score wrong:

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
labctl challenge submit                  # score must reflect what you actually did
labctl challenge abort                   # must undo the setup cleanly
```

## Before you finish

- [ ] `labctl validate` is clean; every `ref` resolves.
- [ ] Every check fails before the work and passes after.
- [ ] Modules leave the lab in the state the next module assumes.
- [ ] `parTime` came from a real timed run.
- [ ] Listed in the table in the relevant README.
- [ ] `make docs-check` passes.
