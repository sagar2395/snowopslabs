---
name: scenario-review
description: "Run the end-to-end review harness on a SnowOps Labs scenario — activate it on a live lab, walk it as a platform engineer, tamper-test its grading, score it out of 5 against the rubric, and improve it until it scores 4.8. Use when asked to test, review, grade, evaluate or improve a scenario end to end, and at the end of authoring a new one."
user-invocable: true
---

# Reviewing a scenario end to end

The workflow is [docs/authoring/scenario-review.md](../../../docs/authoring/scenario-review.md).
Read it first — it is the source of truth for the phases and the rubric, and
this file is only the operating procedure. When the two disagree, the document
wins.

**Input:** a scenario name. **Output:** a score to two decimals, a ranked
findings list, the fixes you landed, and a green `up → verify → down`.

## The one principle

The learner does the platform work; `labctl` stages it and grades it. If a
scenario's objectives are satisfied by typing `labctl` verbs, the review's
verdict on dimension 1 is low no matter how clean everything else is. The
canonical shape is `env-promotion`: labctl builds three environments and grades
the result, and the learner promotes with `kubectl set image` and
`kubectl rollout status`.

## Before you start

```bash
kubectl config current-context     # a live lab is required; this is not hermetic
./bin/labctl status
./bin/labctl scenario list
```

No live lab means no review. Say so and stop rather than scoring from YAML.

Announce a review plan first — which phases will run, roughly how long
activation will take, and anything you will sabotage in P5 — then work through
it without stopping for approval at each phase.

## Running the phases

Work through P0–P7 from the document. Per phase, keep a note file under the
scratchpad with the commands you ran and their output; the rubric is scored from
that file, never from memory or from re-reading the YAML.

The recipes that are easy to get wrong — dashboard panel extraction, the
`promql` probes, the tamper matrix, the leak sweep — are in
[EVIDENCE.md](EVIDENCE.md). The rubric anchors and the arithmetic are in
[RUBRIC.md](RUBRIC.md).

Two phases carry most of the value; do not shorten them:

- **P3 (signal and graph).** Start real load with `labctl traffic`, then prove
  each claimed metric has samples *and* is plotted on a panel that is not empty.
  A scenario whose description promises telemetry and whose dashboards are blank
  fails dimension 3 outright.
- **P5 (tamper test).** Break the subject of every check and confirm `verify`
  goes red. Restore afterwards, and re-run `verify` to prove you restored it.
  This is the only way to tell a graded outcome from a tautology.

## Scoring

Score the eight dimensions from the evidence file, weight them, and report to
two decimals. Rules that keep the number honest:

- Every dimension below 5 carries a named finding. No finding, no deduction.
- A score rises only when a re-run produces new evidence. Never re-score by
  re-reading.
- Report the number you computed. Do not round up to close the loop, and do not
  soften a finding because fixing it is inconvenient.

## The improvement loop

Below 4.8: fix the highest-impact finding, re-run only the phases it touches,
re-score. Repeat.

Stop when the score is **≥ 4.8**, or **≥ 4.7 with no remaining recommendation** —
that tenth of a point is for a scenario as good as its subject allows, not for
one with open findings. If four rounds do not converge, stop and report the
review as failed with the open findings named; that is a real outcome, not a
failure to try harder.

Fixes are real changes to `scenario.yaml`, its assets, its dashboards and the
docs they affect — made under [scenario-author](../scenario-author/SKILL.md)'s
rules, not shortcuts to move the number.

## Finishing

- [ ] `labctl validate` clean.
- [ ] `labctl scenario up → verify → down` green on the current lab, with the
      P5 sabotage restored and re-verified.
- [ ] Every finding either fixed or listed as deferred with a reason.
- [ ] Docs updated in the same change — the catalog entry in
      [docs/scenarios.md](../../../docs/scenarios.md), the schema reference if a
      field's use changed, the runbook if a trap was found.
- [ ] `make docs-check` passes.
- [ ] Final report: score, per-dimension table, findings, fixes landed.
