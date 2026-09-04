---
name: learning-review
description: "Run the end-to-end review harness on a SnowOps Labs learning path or challenge — walk the path cold on a live lab, sweep for checks that are green before the learner works, time a real challenge run to calibrate par, test that a zero-work submit scores nothing, score it out of 5 and improve it until it reaches 4.8. Use when asked to test, review, grade, evaluate or improve a learning path or challenge end to end, and at the end of authoring one."
user-invocable: true
---

# Reviewing a path or challenge end to end

The workflow is [docs/authoring/learning-review.md](../../../docs/authoring/learning-review.md).
Read it first — it is the source of truth for the phases and the rubrics, and
this file is only the operating procedure. When the two disagree, the document
wins.

**Input:** a path or challenge name. **Output:** a score to two decimals, a
ranked findings list, the fixes you landed, and a lab left clean.

## The one principle

Both are thin wrappers over content that already exists, and **neither is better
than what it wraps**. A challenge over an incident inherits that incident's
diagnosability; a path module that runs a scenario inherits that scenario's
grading integrity.

So: **check the underlying content's review status first.** If the wrapped
incident or scenario has not passed [incident-review](../incident-review/SKILL.md)
or [scenario-review](../scenario-review/SKILL.md), say so, and either run that
review first or cap the wrapper's score and record why. Polishing a clock and a
score over a fault nobody can diagnose does not make a fair exam.

What the wrapper owns is its own: a path owns **sequence and explanation**, a
challenge owns **the clock and the score**. That is what you are grading.

## Before you start

```bash
kubectl config current-context      # a live lab is required; this is not hermetic
./bin/labctl status
./bin/labctl learn list
./bin/labctl challenge list
./bin/labctl challenge status       # no challenge active before you begin
```

Confirm the lab is healthy before you start and before you record any finding —
a flapping control plane produces findings that belong to the cluster, not the
content. The check is in [EVIDENCE.md](EVIDENCE.md).

Announce the plan: which track you are running, that you will start and abort
challenges (which inject faults) on their cluster, and roughly how long the cold
walk will take. Then work through it without pausing for approval per phase.

## Which track

- **A path** → phases L0–L7, path rubric.
- **A challenge** → phases C0–C6, challenge rubric.

Do not mix them. If asked to review both, run them as two reviews with two
scores.

## The phases that carry the value

**L2 — the premature-green sweep.** Run every module's check *before* doing that
module's work; all must fail. The classic defect is a check that asserts
something the platform already provides — a module that activates the
observability scenario whose check is `GET http://prometheus/-/ready`, green
before the learner does anything. A path with any premature-green check is
capped at 3.5.

**L3 — the cold walk.** Do the whole path in order using only the intro and the
action ref, timing each module. Every time you look outside the path to proceed
is a finding, and the total is the evidence for `estimatedMinutes`.

**C2 — the zero-work submit.** Start, submit having fixed nothing, and the score
must be at or near zero. A challenge that passes for doing nothing is capped at
3.5.

**C3/C4 — the honest timed run.** Actually diagnose and fix it, timed, with no
hints. The score proves grading works; your elapsed time is the only admissible
evidence for whether `parTime` is calibrated. A par time that cannot be traced
to a real run is a finding whatever the number is.

## Scoring

Score from the evidence file, weight, report to two decimals. Every dimension
below 5 carries a named finding; a score rises only on new evidence. Report the
number you computed — do not round up to close the loop. Apply the two caps.

## The improvement loop

Below 4.8: fix the highest-impact finding, re-run the phases it touches,
re-score. Stop at **≥ 4.8**, or **≥ 4.7 with no remaining recommendation**. Four
rounds without convergence is a failed review, reported with the open findings
named.

Caution: **a fix to the wrapped content re-runs the wrapper's review.** Retuning
an incident's detection check changes what `challenge submit` grades and what a
path's incident module proves. Fixes are made under
[learning-author](../learning-author/SKILL.md)'s rules.

## Finishing

- [ ] `labctl validate` clean; every `ref` resolves.
- [ ] The lab is clean: no active challenge or incident, no `labfault-*`
      residue, path progress reset if you consumed it.
- [ ] Every finding either fixed or listed as deferred with a reason.
- [ ] Docs updated in the same change — the table in
      [learn/README.md](../../../learn/README.md) or
      [challenges/README.md](../../../challenges/README.md).
- [ ] `make docs-check` passes.
- [ ] Final report: score, per-dimension table, findings, fixes landed, the
      timed-run figures behind any `parTime` or `estimatedMinutes` change, and
      what you left running.
