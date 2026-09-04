# Rubrics

Two rubrics — a path is a curriculum, a challenge is an exam. The authoritative
tables are in
[docs/authoring/learning-review.md](../../../docs/authoring/learning-review.md#the-rubrics);
this file adds the anchors that make two reviewers land on the same number.

`score = Σ(dimension × weight)`, reported to two decimals.

---

## Path

| # | Dimension | Weight | Scored from |
|---|---|---|---|
| 1 | Check integrity | 0.20 | L2 |
| 2 | Teaching quality | 0.20 | L5 |
| 3 | State coherence | 0.15 | L4 |
| 4 | Arc & outcome | 0.15 | L0, L7 |
| 5 | Time honesty | 0.10 | L3 |
| 6 | Resumability | 0.10 | L6 |
| 7 | Compliance & self-sufficiency | 0.10 | L1, L3 |

**1 — Check integrity.** 5: every module's check failed before its work and
passed after. 4: all honest, but one takes longer to turn than its
`timeoutSeconds` suggests. 3: one check passes for a reason broader than the
module's work — it would also be green after a different module. 2: a check is
green from the platform alone before the learner starts. 1: multiple modules
advance without the learner doing anything. **Any premature green caps the
overall score at 3.5.**

**2 — Teaching quality.** 5: every intro explains why the module exists and what
to look at, and difficulty climbs across the path. 4: one intro is thin. 3: an
intro paraphrases the command in `action.ref` instead of explaining it. 2: a
module has no intro and no context — a command to paste. 1: the learner finishes
able to repeat the commands and unable to explain any of them.

**3 — State coherence.** 5: every module runs in the state its predecessor left,
and `learn next` refuses to skip. 3: one module works only because of something
the path never established — it passes on the author's lab and fails on a fresh
one. 1: a module tears down what a later module needs.

**4 — Arc & outcome.** 5: someone who finished has the skill the description
promised. 3: the path tours the lab — installs things and verifies things —
without asking the learner to interpret anything. 1: the description promises a
skill the path never exercises.

**5 — Time honesty.** 5: the cold walk landed within about 25% of
`estimatedMinutes`. 3: off by roughly double. 1: off by enough that someone
would abandon it part-way, which is worse than not starting.

**6 — Resumability.** 5: progress survives a restart, `progress` is accurate
mid-path, completed modules are safe to re-run. 3: progress is right but a
completed module errors when re-run. 1: progress is lost or wrong.

**7 — Compliance & self-sufficiency.** 5: refs resolve, intros and checks stay
inside the path directory, no external URL or credential is assumed, and the
walk needed nothing outside the path. Each breach costs a point; a path
depending on a credential that will not exist on every lab machine caps this
at 2.

---

## Challenge

| # | Dimension | Weight | Scored from |
|---|---|---|---|
| 1 | Grading integrity | 0.25 | C2, C3, C5 |
| 2 | Par-time calibration | 0.20 | C3, C4 |
| 3 | Underlying content | 0.15 | C0 |
| 4 | Completability | 0.15 | C3 |
| 5 | Abort & cleanliness | 0.10 | C6 |
| 6 | Framing | 0.10 | C0, C3 |
| 7 | Invariant compliance | 0.10 | C1 |

**1 — Grading integrity.** 5: the zero-work submit scored ~0, a genuine fix
scored high, and the hint, time and check-ratio terms all matched the published
formula. 4: all correct, but the time deduction does not visibly cap at 20. 3: a
partial fix scores the same as a complete one. 2: hints do not deduct, or the
deduction is not `hintPenalty`. 1: a zero-work submit passes. **A passing
zero-work submit caps the overall score at 3.5.**

**2 — Par-time calibration.** 5: `parTime` is traceable to a real timed run and
reachable with one or two lookups. 4: defensible but never actually timed —
record the run you did as the evidence. 3: tight enough that a competent run
takes the full time deduction, or loose enough that the clock never bites. 1: a
par a correct solution cannot meet.

**3 — Underlying content.** 5: the wrapped incident or scenario has passed its
own review at 4.8+. 4: it has not been reviewed, but this review found nothing
against it. 3: it has open findings that do not affect this challenge. 1: it has
open findings that make the challenge unfair — a fault that cannot be diagnosed
from the lab's observability. **Score this from the other review, not from a
fresh opinion**, and name which review you relied on.

**4 — Completability.** 5: startable and finishable on a fresh lab, start to
submit, without outside help. 3: needs a prerequisite the challenge does not
declare. 1: cannot be completed as shipped.

**5 — Abort & cleanliness.** 5: `abort` undoes the setup from any state, records
the run, and leaves no `labfault-*` annotation, orphaned namespace or armed
PrometheusRule. 3: clean but needs a second attempt. 1: `abort` leaves the lab
broken — the worst outcome here, because the learner reached for the escape
hatch.

**6 — Framing.** 5: the description states the task and the stakes in the
victim's language, without naming the mechanism. 3: flat but accurate. 2: it
names the root cause, so there is nothing left to diagnose. 1: it describes a
symptom the challenge does not actually produce.

**7 — Invariant compliance.** 5: `grading.useDetectionCheck: true` for an
incident challenge (or a stated reason not to), README table matching the YAML,
one-active-challenge honoured. Each breach costs a point; hand-written grading
checks that disagree with `labctl incident status` cap this at 2.

---

## Deduction discipline

- A deduction needs a finding: what you observed, the command that showed it,
  and what a learner would experience.
- One defect deducts once. A path module whose check is premature-green is a
  dimension-1 finding, not also a dimension-4 one — cross-reference instead.
- **Do not re-litigate the wrapped content.** If the incident is weak, that is
  dimension 3 and a pointer to the incident's own review; it is not licence to
  mark the challenge down twice.
- Timing evidence is the reviewer's own run. A `parTime` or `estimatedMinutes`
  you did not measure is not evidence, and saying so is a finding.
- Absence of evidence is not a 5. A phase you did not run is stated as a gap.
