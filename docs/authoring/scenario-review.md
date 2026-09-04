# Scenario review

The end-to-end quality gate for a scenario. `labctl validate` proves a scenario
is *well-formed*; this proves it is *worth a platform engineer's afternoon*.

It is a workflow, not a binary: seven phases run against a live lab, each one
producing evidence, and the evidence feeds a weighted rubric that scores the
scenario out of 5. A scenario ships at **4.8 or above**. It may ship at 4.7 when
the review has no remaining recommendation to make — the tenth of a point is
grace for a scenario that is as good as its subject allows, never for one with
known gaps left unaddressed.

Run it with the `scenario-review` skill, or by hand from this document.

> There is deliberately no `labctl scenario review`. Reviewing content is
> judgement applied to evidence, and the evidence is gathered with the same
> `kubectl`, `promql` and `labctl` commands a learner uses. Encoding the
> judgement in Go would freeze it; encoding it here keeps it reviewable.

---

## The principle being enforced

**The learner does the platform work. `labctl` sets the stage and grades the
result.**

A scenario is good when someone finishes it having exercised a skill that
transfers to a real cluster — a weighted route shifted with `kubectl apply`, a
rollout promoted and rolled back, a PodDisruptionBudget that actually held
during a drain. It is bad when the transferable skill is "I typed a `labctl`
verb". This is why there is no `labctl env promote`
([R06](../runbooks/R06-multi-env-promotion.md)): promotion is `kubectl set
image` and `kubectl rollout status`, and labctl's job is to build the three
environments and grade the result.

Four corollaries the review tests directly:

1. **Every objective maps to work the learner performs**, not to something a
   stage installed for them.
2. **Every claim the scenario makes is observable** — and observable on a graph,
   not only in a JSON blob. "Watch the split in mesh telemetry" is a promise;
   a populated Grafana panel is the delivery.
3. **Every check grades the outcome**, so breaking the outcome turns the check
   red. A check that survives sabotage is not grading anything.
4. **The scenario is self-sufficient**: `labctl scenario info`, the `explore`
   block, the `references` and the failing checks' `remediation` are enough to
   finish it without reading this repository.

---

## The seven phases

Run them in order. A phase's evidence is the only admissible input to the
rubric — no dimension is scored from reading YAML alone except P1.

### P0 — Read the contract

Read `scenario.yaml`, every asset it references, its entry in
[docs/scenarios.md](../scenarios.md), and the
[schema](../reference/scenario-schema.md). Write down, before touching the
cluster, what the scenario *claims*: each objective, each metric it says you can
watch, each skill it says you will practise. That list is what the later phases
are testing against.

### P1 — Static validation

```bash
labctl validate
labctl scenario info <name>
```

Confirm: every `helm` component has a `version` pinned in
`config/versions.env`; platform-owned values come through `platformValues:`
rather than a second copy; every `script` component has an `uninstallScript`;
every `snippets[].path` and `references[].url` resolves; the scenario appears in
the catalog. `info` is the learner's first contact — read it as one.

### P2 — Cold activation

From a torn-down state, and timed:

```bash
labctl scenario down <name> || true
time labctl scenario up <name> --deploy-prereqs
labctl scenario verify <name> --watch --timeout 10m
```

Record how long it takes to reach all-green, and every point where activation
demanded something the scenario never told the learner about — a missing app, a
provider that had to be switched, a namespace label. Those are defects, not
prerequisites.

### P3 — Signal and graph

A scenario that promises telemetry must deliver a populated graph. For each
metric family the scenario names, and for every dashboard in `explore.urls`:

```bash
# the metric the scenario claims exists, actually exists and has samples
curl -s --get http://prometheus.$DOMAIN_SUFFIX/api/v1/query \
  --data-urlencode 'query=sum by (destination_version) (rate(istio_requests_total[5m]))'

# every panel query on the linked dashboard returns data, not an empty series
curl -s -u admin:admin http://grafana.$DOMAIN_SUFFIX/api/dashboards/uid/<uid> \
  | grep -o '"expr":"[^"]*"'
```

Generate the load with the traffic generator, never a shell loop:

```bash
labctl traffic start --profile browse --rps 20
```

Three failure modes to look for, all of which read to a learner as "this lab is
broken":

- The metric does not exist — a scrape or a ServiceMonitor selector is wrong
  ([R13](../runbooks/R13-observability-pipeline.md)).
- The metric exists but no dashboard plots it, so the promise in the description
  has no destination.
- The dashboard exists but its panels are empty because nothing generates load
  and the scenario never says to start any.

### P4 — Walk it as a platform engineer

Do the scenario, from `info` alone. Run every `explore.commands` entry verbatim
and confirm each one produces the output its label promises. Then perform the
work the objectives describe using real tools — shift the weights and re-apply,
promote the image forward, drain the node — and note every moment you had to
open the repository to proceed. Each one is a self-sufficiency defect.

### P5 — Tamper test the grading

The highest-value phase. For each check, break the thing it claims to grade and
re-run `verify`:

| Claim | Sabotage | `verify` must |
|---|---|---|
| traffic splits 90/10 | set both weights to 100/0 | fail |
| a latency fault is active | delete the fault manifest | fail |
| mTLS is STRICT | set the mode to `PERMISSIVE` | fail |
| the metric is being scraped | scale the exporter to zero | fail |

Restore state after each. **A check that stays green through the sabotage of
its own subject is a tautology** — it asserts that a stage installed something,
which was never in doubt. Replace it with a `promql` or `script` check on the
observable outcome, and give it a `remediation`.

### P6 — Teardown, idempotency and leaks

```bash
labctl scenario up <name>            # again: must converge, not error
labctl scenario reset <name>
labctl scenario down <name>
kubectl get ns; helm list -A; kubectl get pvc -A
```

Nothing the scenario created may survive `down` — except releases it took with
`adopt: true`, which it must not remove.

### P7 — The flow, end to end

Re-read `labctl scenario info <name>` and the UI's scenario page against
everything you now know. Objectives → stages → explore → checks should read as
one story with no dead ends: nothing installed that no objective uses, no
objective with no way to observe it, no check whose failure leaves the learner
without a next step.

---

## The rubric

Eight dimensions, each scored 0–5 against the anchors below, combined by weight.

| # | Dimension | Weight | What a 5 looks like |
|---|---|---|---|
| 1 | **Real-skill fidelity** | 0.20 | Every objective is work the learner performs with real tools; labctl only stages and grades |
| 2 | **Grading integrity** | 0.20 | Every check survives P5 — sabotage turns it red; no tautologies; failures carry `remediation` |
| 3 | **Observability & visualisation** | 0.15 | Every claimed signal exists, is plotted on a linked dashboard, and the panels are populated under the scenario's own traffic |
| 4 | **Narrative clarity** | 0.15 | `info` alone explains why each stage exists and what to look at next; no dead ends |
| 5 | **Production realism** | 0.10 | The change or failure is one that occurs in real clusters, at a realistic shape |
| 6 | **Lifecycle hygiene** | 0.10 | `up → verify → down` clean and repeatable; idempotent; zero leaks |
| 7 | **Invariant compliance** | 0.05 | Pins, `platformValues`, `uninstallScript`, `labctl traffic` over curl loops, docs in step |
| 8 | **Self-sufficiency** | 0.05 | Finishable from `info`, `explore`, `references` and remediations alone |

**Anchors.** 5 — no finding. 4 — one cosmetic finding. 3 — a finding a learner
would notice. 2 — a finding that misleads a learner. 1 — the dimension's promise
is not delivered. 0 — the scenario actively teaches the wrong thing.

Score to two decimals. Every dimension below 5 needs a named finding recorded
with it; a score may only rise when new evidence from a re-run says it should.

## The loop

1. Run P0–P7, score, and list findings ranked by score impact.
2. Below 4.8: fix the highest-impact finding, re-run the phases it touches, and
   re-score from the new evidence.
3. Repeat. Stop at 4.8, or at 4.7 with no remaining recommendation.
4. Every change is a real change to the scenario and the docs it affects — the
   review ends by leaving `labctl validate`, `make docs-check` and a full
   `up → verify → down` green.

A review that cannot get past 4.7 with recommendations still open is reported as
a failed review, with the open findings named. Scores are not rounded up to
close a review.

## Related

- [Incident review](incident-review.md) — the same gate for a fault, where the
  skill under test is diagnosis rather than construction
- [Learning review](learning-review.md) — the same gate for the paths and
  challenges that wrap this scenario
- [Your first scenario](first-scenario.md) — write one
- [Scenario schema](../reference/scenario-schema.md) — the field reference
- [R13](../runbooks/R13-observability-pipeline.md) — why a metric is missing
- [TESTING.md](../TESTING.md) — where this sits relative to the four test layers
