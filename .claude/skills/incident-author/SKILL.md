---
name: incident-author
description: "Write, edit, debug or review a SnowOps Labs incident (fault) — fault.yaml, inject.sh, resolve.sh, hints, solution, detection checks and paging drills. Use whenever a task involves anything under incidents/, a fault.yaml, `labctl incident inject|status|resolve`, or a fault that will not resolve cleanly."
user-invocable: true
---

# Authoring an incident

An incident is a **realistic, reversible** production fault. Inject it, let the
lab break, diagnose it, fix it — and the detection check confirms the fix.

## Read first

1. **[incidents/README.md](../../../incidents/README.md)** — the fault contract:
   every file a fault directory must contain, and the `fault.yaml` schema.
2. [docs/reference/cli/incidents.md](../../../docs/reference/cli/incidents.md) —
   the commands and how timing and history are recorded.
3. The closest existing fault. `service-selector-broken` is the subtle one;
   `oom-kill` is the one with a paging drill.

Checks, references and snippets share the scenario shapes — see
[the scenario schema](../../../docs/reference/scenario-schema.md).

## What makes an incident worth doing

Decide these before writing `inject.sh`. An incident that fails them is
reversible and worthless, and the review in
[docs/authoring/incident-review.md](../../../docs/authoring/incident-review.md)
scores exactly this.

**The learner diagnoses; labctl breaks the lab and grades the fix.** The skill
that transfers is the search, not the patch. The fix is usually one
`kubectl patch`; the value is the twenty minutes before it, spent reading
endpoints, events, dashboards and logs. Design the *symptom* first and the
injection second.

**The symptom must be discoverable through observability.** A dashboard, an
alert, an event, a log line — something the lab already collects must point at
the fault. If the only route to the answer is reading `inject.sh`, you have
written a puzzle, not a drill. `service-selector-broken` is the shape to copy:
the ingress 503s, every pod is Ready, and `kubectl get endpoints` tells the
story.

**The detection check must be honest in both directions.** Green on a healthy
lab, red the moment the fault lands, and green again only for a genuine fix.
Test all three; the first is the one everyone skips.

**A fix you did not imagine is still a fix.** If the learner restarts the
deployment or re-applies the chart instead of patching the field you expected,
the check must go green. A check that recognises only one spelling of the fix
teaches learners to distrust the grader.

## The five rules

1. **Target only the demo apps** (`go-api`, `echo-server`) or a dedicated fault
   namespace. Never platform components, never `kube-system`.
2. **Record what you change.** Annotate the touched resource with
   `labfault-<name>=...` and stash originals in `labfault-<name>-original-*`, so
   `resolve.sh` can undo it without guessing.
3. **`resolve.sh` must never fail the user.** It runs after any amount of manual
   fixing. Every step tolerates "already fixed" — `--ignore-not-found`, guards,
   `|| true` where it is genuinely safe.
4. **`inject.sh` is idempotent.** Re-running while injected is a no-op.
5. **Write the detection check as "what must be true when healthy."** It is both
   the resolution detector and the challenge grader, so it has to be honest in
   both directions: it must fail while the fault is live and pass once — and
   only once — the user has really fixed it. Test that claim two ways. *False
   green:* satisfy the check without repairing the cause — scale a decoy in to
   answer the probe, patch the field the check reads — and confirm it stays red.
   *False red:* fix the fault by a route you did not plan for — a rollout
   restart, a chart re-apply — and confirm it goes green.

Hints go from gentle nudge to near-answer. The last hint may name the resource;
the solution names the command.

## Paging drills

A fault with `expectAlert` must ship `alerts/rule.yaml`: a PrometheusRule
labelled `release: prometheus` so kube-prometheus-stack loads it, whose alert
carries `labfault: "true"` so Alertmanager routes it to the pager.

`inject.sh` arms it (tolerating a missing monitoring stack), `resolve.sh`
disarms it, and `labctl incident status` reports whether the page fired by
querying `ALERTMANAGER_URL`.

## Verify your work

```bash
labctl validate
labctl incident inject my-fault
labctl incident status              # must FAIL while the fault is live
# ...fix it by hand, the way a learner would...
labctl incident status              # must PASS once genuinely fixed
labctl incident resolve             # must restore the lab from any state
```

Then prove the escape hatch independently from all four states a learner can
leave behind: freshly injected, half fixed by hand, fully fixed by hand, and
with the active state lost (`labctl incident resolve <name>`). The half-fixed
path is the one that breaks naive resolve scripts.

## Then review it

A reversible fault that resolves cleanly is not yet a good drill. Hand every new
or materially changed incident to the
[incident-review](../incident-review/SKILL.md) skill, which injects it on a live
lab, diagnoses it blind, walks the hint ladder, false-fix tests the detection
check in both directions, exercises the escape hatch from every state, scores it
out of 5 and improves it until it reaches 4.8.

```
/incident-review my-fault
```

Treat the score as part of the definition of done: below 4.8 the incident is a
draft, whatever `validate` says. Note that the reviewer gets **one** blind
diagnosis per fault — once they have read `inject.sh`, diagnosability can no
longer be scored honestly.

## Before you finish

- [ ] `fault.yaml`, `inject.sh`, `resolve.sh`, `hints.md`, `solution.md` all present.
- [ ] `inject.sh` is idempotent; `resolve.sh` survives a partial manual fix.
- [ ] Detection check passes on a healthy lab, fails while broken, and passes
      when fixed.
- [ ] False-green and false-red tested: the check cannot be satisfied without a
      real fix, and an unexpected-but-real fix still turns it green.
- [ ] The symptom is findable through the lab's observability, without reading
      `inject.sh`.
- [ ] `resolve` works from all four states, leaving no `labfault-*` residue.
- [ ] Scripts are POSIX and pass `make lint-shell`.
- [ ] `alerts/rule.yaml` present if `expectAlert` is set.
- [ ] Listed in the fault table in [incidents/README.md](../../../incidents/README.md).
- [ ] `incident-review` scores it 4.8 or better.
- [ ] `make docs-check` passes.
