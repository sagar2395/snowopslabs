# Rubric

Eight dimensions, scored 0–5 from phase evidence, combined by weight. The
authoritative table is in
[docs/authoring/incident-review.md](../../../docs/authoring/incident-review.md#the-rubric);
this file adds the anchors that make two reviewers land on the same number.

| # | Dimension | Weight | Scored from |
|---|---|---|---|
| 1 | Detection integrity | 0.20 | I2, I3, I5, I6 |
| 2 | Diagnosability | 0.20 | I4 |
| 3 | Reversibility & safety | 0.15 | I3, I7 |
| 4 | Hint ladder & solution | 0.15 | I5 |
| 5 | Production realism | 0.10 | I0, I3 |
| 6 | Observability & paging | 0.10 | I4 |
| 7 | Invariant compliance | 0.05 | I1 |
| 8 | Self-sufficiency | 0.05 | I5 |

`score = Σ(dimension × weight)`, reported to two decimals.

## Anchors

**1 — Detection integrity.** 5: green on a healthy lab, red the instant the
fault lands, green only on a genuine fix, and both halves of I6 behaved. 4: all
of that, but the check takes longer to turn than its `timeoutSeconds` suggests.
3: a false red — one legitimate route to the fix leaves the check red. 2: a
false green — the check can be satisfied while the fault is live. 1: the check
never goes red, or never goes green.

**2 — Diagnosability.** 5: found from the symptom and the lab's observability
inside the time box, and the signal that gave it away is one a real on-call
engineer would reach for. 4: found, but only after checking a layer the
description pointed away from. 3: found only by `kubectl get`-ing every object
in the namespace. 2: found only by guessing what a fault library would do. 1:
not findable without reading `inject.sh`.

**3 — Reversibility & safety.** 5: all four escape-hatch states restore cleanly,
`inject` is idempotent, blast radius is a demo app or a `labfault-*` namespace,
no residue. 4: clean, but `resolve` prints a scary error it then recovers from.
3: one state needs a second `resolve` to converge. 2: `labfault-*` annotations,
a namespace or an armed PrometheusRule survive. 1: `resolve` fails from the
half-fixed state, or the fault touched a platform component.

**4 — Hint ladder & solution.** 5: each hint adds exactly one rung — where to
look, then what is wrong, then which resource — and the solution names a command
that works verbatim. 4: the last hint overlaps the solution. 3: a hint repeats
its predecessor, or the ladder is two rungs where it needs three. 2: hint 1
names the resource, collapsing the drill. 1: the solution's command does not
work, or contradicts the hints.

**5 — Production realism.** 5: a failure that really happens, presenting the way
it really presents — healthy-looking pods behind an empty Service, a rollout
wedged on a tag that does not exist. 3: real in kind, cartoonish in shape
(a resource limit so low nothing could ever start). 1: a failure mode no cluster
produces, or one only this lab can produce.

**6 — Observability & paging.** 5: the symptom is visible on a dashboard or in
logs without knowing what to look for, and an `expectAlert` fault pages through
Alertmanager and disarms on resolve. 4: visible, but only once traffic is
running and the incident does not say to start any. 3: visible only in
`kubectl describe` output. 2: `expectAlert` is declared and the page never
fires. 1: the fault is invisible to every signal the lab collects.

**7 — Invariant compliance.** 5: POSIX shell, `make lint-shell` clean, every
change recorded via `labfault-*` annotations, the README fault table current,
`alerts/rule.yaml` present iff `expectAlert` is set. Each breach costs a point;
touching a platform component or `kube-system` caps this at 1.

**8 — Self-sufficiency.** 5: diagnosed and fixed from `info`, hints, references
and snippets, with the repository closed. 3: one lookup into the incident's
source was needed. 1: undoable without reading `inject.sh`.

## Deduction discipline

- A deduction needs a finding: what you observed, the command that showed it,
  and what a learner would experience.
- One defect deducts once. A fault that is invisible to observability is a
  dimension-6 finding *or* a dimension-2 finding — pick the one it most belongs
  to, and cross-reference the other.
- **A false green outranks everything.** An incident whose check can be
  satisfied without fixing the fault cannot score above 3.5 overall, whatever
  the other dimensions say — it is a grader that does not grade.
- Diagnosability is scored once, from the first blind run. A re-score after you
  know the answer is not evidence.
- Absence of evidence is not a 5. A phase you did not run is stated as a gap in
  the report.
