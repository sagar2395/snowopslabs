---
name: incident-review
description: "Run the end-to-end review harness on a SnowOps Labs incident — inject it on a live lab, diagnose it blind, walk the hint ladder, false-fix test the detection check, exercise the escape hatch from every state, score it out of 5 and improve it until it reaches 4.8. Use when asked to test, review, grade, evaluate or improve an incident or fault end to end, and at the end of authoring a new one."
user-invocable: true
---

# Reviewing an incident end to end

The workflow is [docs/authoring/incident-review.md](../../../docs/authoring/incident-review.md).
Read it first — it is the source of truth for the phases and the rubric, and
this file is only the operating procedure. When the two disagree, the document
wins.

**Input:** an incident name. **Output:** a score to two decimals, a ranked
findings list, the fixes you landed, and a lab left clean.

## The one principle

The learner diagnoses; `labctl` breaks the lab and grades the fix. The skill
being taught is the search, not the patch — so the two dimensions that carry
0.40 of the score are whether the fault is **findable** from its symptom, and
whether the detection check is **honest** about when it has been fixed.

## Before you start

```bash
kubectl config current-context      # a live lab is required; this is not hermetic
./bin/labctl status
./bin/labctl incident list
./bin/labctl incident status        # must be "no incident active" before you begin
```

No live lab means no review. An incident already active means resolve it first,
or you are reviewing two faults at once.

Announce the plan before you start: which phases will run, that you will inject
the fault on their cluster, what the blast radius is, and that you will resolve
and sweep at the end. Then work through it without pausing for approval at each
phase.

## The rule that makes I4 worth anything

**Do not read `inject.sh`, `manifests/` or `solution.md` until phase I4 is
finished.** Read `fault.yaml`'s description and nothing more. Diagnosability is
0.20 of the score and it cannot be assessed by someone who already knows the
answer — and unlike every other phase, you get one attempt at it per incident.

If you have already read the fault's source in this session, say so in the
report and score dimension 2 as "not independently assessable" rather than
guessing.

## Running the phases

Work through I0–I7 from the document, keeping a note file under the scratchpad
with every command and its output. The rubric is scored from that file.

The recipes that are easy to get wrong — the false-fix matrix, the escape-hatch
states, the residue sweep, the paging probe — are in [EVIDENCE.md](EVIDENCE.md).
The anchors and the arithmetic are in [RUBRIC.md](RUBRIC.md).

Three phases carry the value; do not shorten them:

- **I2 (baseline).** Prove the check is green on a healthy lab *before* you
  trust anything it says while injected.
- **I4 (blind diagnosis).** Time-box it, work only from observability, and
  record which signal gave it away. "Nothing did" is the finding.
- **I6 (false-fix).** Both directions. A false green means the grader can be
  satisfied without fixing anything; a false red means a correct fix is marked
  wrong, which is worse — it teaches learners to distrust the grader.

## Scoring

Score the eight dimensions from the evidence file, weight them, report to two
decimals. Every dimension below 5 carries a named finding; a score rises only on
new evidence. Report the number you computed — do not round up to close the
loop.

## The improvement loop

Below 4.8: fix the highest-impact finding, re-run the phases it touches,
re-score. Stop at **≥ 4.8**, or **≥ 4.7 with no remaining recommendation**. Four
rounds without convergence is a failed review, reported with the open findings
named.

Two cautions specific to incidents:

- Re-running I4 after you know the answer cannot re-score diagnosability. Say in
  the report that it was scored from the first run.
- Any change to `resolve.sh` re-runs the whole of I7. The states interact.

Fixes are real changes made under [incident-author](../incident-author/SKILL.md)'s
five rules — especially rule 3, that `resolve.sh` must never fail the user.

## Finishing

- [ ] `labctl validate` and `make lint-shell` clean.
- [ ] The lab is clean: no active incident, no `labfault-*` residue, no armed
      PrometheusRule, the target workload healthy.
- [ ] Every finding either fixed or listed as deferred with a reason.
- [ ] Docs updated in the same change — the fault table in
      [incidents/README.md](../../../incidents/README.md), and the runbook if a
      trap was found.
- [ ] `make docs-check` passes.
- [ ] Final report: score, per-dimension table, findings, fixes landed, and what
      you left running.
