# Rubric

Eight dimensions, scored 0–5 from phase evidence, combined by weight. The
authoritative table is in
[docs/authoring/scenario-review.md](../../../docs/authoring/scenario-review.md#the-rubric);
this file adds the anchors that make two reviewers land on the same number.

| # | Dimension | Weight | Scored from |
|---|---|---|---|
| 1 | Real-skill fidelity | 0.20 | P0, P4 |
| 2 | Grading integrity | 0.20 | P5 |
| 3 | Observability & visualisation | 0.15 | P3 |
| 4 | Narrative clarity | 0.15 | P1, P7 |
| 5 | Production realism | 0.10 | P0, P4 |
| 6 | Lifecycle hygiene | 0.10 | P2, P6 |
| 7 | Invariant compliance | 0.05 | P1 |
| 8 | Self-sufficiency | 0.05 | P4 |

`score = Σ(dimension × weight)`, reported to two decimals.

## Anchors

**1 — Real-skill fidelity.** 5: every objective is performed by the learner with
`kubectl`/`helm`/`istioctl`/`kubectl rollout`, and labctl only stages and
grades. 4: one objective is satisfied by activation alone. 3: the headline skill
is real but the drill stops before the interesting half — a canary that is never
promoted, a backup that is never restored. 2: most objectives are satisfied by
`scenario up`. 1: the transferable skill is "run a labctl verb".

**2 — Grading integrity.** 5: every check went red under P5 sabotage, and every
failure printed a `remediation` that names the fix. 4: all checks grade the
outcome, one lacks remediation. 3: one check is a tautology — it asserts a stage
installed something. 2: the headline objective has no check that can fail. 1:
`verify` is green on a scenario whose promise is broken.

**3 — Observability & visualisation.** 5: every claimed signal has samples under
the scenario's own traffic, is plotted on a dashboard the scenario links, and a
`promql` check asserts the metric exists. 4: all of that, but a panel needs a
time-range nudge to look right. 3: the metric exists and is checked, but nothing
plots it — the learner reads numbers out of `curl`. 2: a linked dashboard's
panels are empty because the scenario never says to generate load. 1: the
described signal does not exist in Prometheus at all.

**4 — Narrative clarity.** 5: `info` alone explains why each stage exists, what
to look at, and what "done" means. 4: one `explore` entry needs a sentence more.
3: a stage exists that no objective refers to, or an objective with nothing to
observe. 2: the learner cannot tell what to do after activation. 1: the
description promises something the scenario does not do.

**5 — Production realism.** 5: the change or failure and its shape match what
happens on a real cluster — a 90/10 canary, a p99 regression under load, a drain
that a PDB resists. 3: real in kind, toy in shape (a two-pod "load test", a
fault too obvious to diagnose). 1: an artificial failure no cluster produces.

**6 — Lifecycle hygiene.** 5: cold `up` converges unattended, a second `up` is a
no-op, `reset` is fast, `down` leaves nothing behind. 4: converges but takes
long enough to need a note in the docs. 3: needs one manual step the scenario
does not document. 2: `down` leaks a namespace, release or PVC. 1: `up` fails
from a cold lab.

**7 — Invariant compliance.** 5: chart versions pinned in `config/versions.env`,
platform values via `platformValues:`, every `script` component reversed by an
`uninstallScript`, load via `labctl traffic`, docs in step. Each breach costs a
point; a duplicated platform values file or an unpinned chart caps this at 2.

**8 — Self-sufficiency.** 5: finished from `info`, `explore`, `references` and
remediations, with the repository closed. 3: one lookup into the repo was
needed. 1: undoable without reading the scenario's source.

## Deduction discipline

- A deduction needs a finding: what you observed, the command that showed it,
  and what a learner would experience.
- One defect deducts once, in the dimension it most belongs to. Do not charge a
  missing dashboard to both 3 and 4.
- Weight the *learner's* experience, not the author's effort. An elegant
  scenario nobody can follow scores badly on 4 and 8.
- Absence of evidence is not a 5. A phase you did not run is scored from the
  phases you did, and the gap is stated in the report.
