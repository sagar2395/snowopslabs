---
name: scenario-author
description: "Write, edit, debug or review a SnowOps Labs scenario — scenario.yaml, stages, components, checks, parameters, values ownership and chart pinning. Use whenever a task involves anything under scenarios/, a scenario.yaml, `labctl scenario up|verify|new`, or a scenario check that fails or passes when it should not."
user-invocable: true
---

# Authoring a scenario

## Read first, in this order

1. **[docs/reference/scenario-schema.md](../../../docs/reference/scenario-schema.md)** — the
   complete field reference. Trust it; do not read `src/pkg/scenario/schema.go`
   to confirm a field exists.
2. [docs/authoring/first-scenario.md](../../../docs/authoring/first-scenario.md)
   — the walkthrough, if you are starting from nothing.
3. An existing scenario closest to the one you are writing. `observability-sre`
   is the richest example; `autoscaling-under-load` is the one with parameters.

Read the engine (`src/internal/scenario/`) only if the schema doc cannot answer
your question — and if that happens, fix the schema doc in the same change.

## Start from the scaffold

```bash
labctl scenario new my-scenario     # valid and verify-green immediately
```

Never hand-write the directory. The scaffold gives you a passing readiness
check, which is the shape every other check should follow.

## What makes a scenario worth doing

Before any YAML, decide these four. A scenario that fails them is well-formed
and worthless, and the review in
[docs/authoring/scenario-review.md](../../../docs/authoring/scenario-review.md)
scores exactly this.

**The learner does the platform work; labctl stages it and grades it.** Every
objective should be something they perform with `kubectl`, `helm`, `istioctl`,
`kubectl rollout` — the commands that transfer to a real cluster. labctl's job
is to build the environment and grade the result. `env-promotion` is the shape
to copy: there is deliberately no `labctl env promote`, because the skill being
taught is `kubectl set image` and `kubectl rollout status`. If your objectives
are satisfied by typing `scenario up`, there is no scenario yet.

**Every claim is observable, on a graph.** If the description says "watch the
split in mesh telemetry", a linked Grafana dashboard must plot that split and be
populated under the scenario's own traffic. Ship the dashboard with the scenario
(`type: grafana-dashboard`), tell the learner to start `labctl traffic`, and add
a `promql` check that the metric exists — otherwise a broken scrape reads as an
empty panel the learner debugs alone.

**Every check grades an outcome, so breaking the outcome turns it red.** Write
the check, then sabotage the thing it claims to grade and confirm `verify`
fails. A check that survives that is asserting a stage installed something,
which was never in doubt.

**The scenario is self-sufficient.** `labctl scenario info`, `explore`,
`references` and the failing checks' `remediation` must be enough to finish it
with this repository closed.

## The rules that bite

**One values file per component.** A component's Helm values live once, at
`platform/<category>/<component>/values.yaml`. Point at it with
`platformValues:` and put only your differences in `valuesFile:`. Never copy the
platform's values into the scenario — the copy drifts, and Helm reports the
drift as a forbidden update to an immutable StatefulSet field (ADR-0010).

**Every chart is pinned.** `version:` is required on every `helm` component, and
the pin lives in `config/versions.env`. Adding a component means adding its pin
in the same change (ADR-0011).

**Adopt what the platform owns.** `adopt: true` reuses an installed release
instead of upgrading it. A scenario that adopts a release must **not** uninstall
it on teardown.

**Undo your scripts.** A `script` component needs an `uninstallScript`, or its
side effects outlive the scenario.

**Use `labctl traffic`, never a curl loop.** It runs k6 in-cluster and
remote-writes the client-side metrics that dashboards compare against the app's
own counters.

## Writing checks

Checks are the grading primitive. Write them as "what must be true when this
scenario is healthy", not "what did I just install".

- Assert the **metric exists**, not just that the pod is running. An empty
  dashboard should fail `verify` rather than quietly confuse a learner.
- Give every check that a user can act on a `remediation`. It prints under
  **Next step(s)** instead of the generic "a pod may still be starting" hint.
- Mark a check `pending: true` when it represents a drill step the user has not
  performed yet. It renders PENDING rather than FAIL, so "you have not run the
  backup" does not read as "the scenario is broken".
- Comparing a running image? Use `.spec.containers[].image`.
  `.status.containerStatuses[].image` reports whichever tag the kubelet
  resolved, so it says `:latest` for a pod that asked for `:v1.1.0` when the
  tags share a digest.
- Avoid tautologies. A check that asserts a file you just applied exists proves
  nothing about the pipeline. The test is mechanical: break the thing the check
  claims to grade — flip the weights to 100/0, delete the fault, set mTLS to
  `PERMISSIVE` — and re-run `verify`. If it stays green, replace the check with
  a `promql` or `script` check on the observable outcome.

## Verify your work

```bash
labctl validate                       # schema, cross-references, templates
labctl scenario up my-scenario
labctl scenario verify my-scenario    # every check must pass
labctl scenario down my-scenario      # and tear down cleanly
labctl scenario reset my-scenario     # fast retry, no lab teardown
```

A scenario is not done until `up → verify → down` is clean on a fresh cluster.
Only then is `verified: true` honest — and that flag is curation metadata for
the nightly e2e job, not a user-facing badge.

## Then review it

A clean lifecycle proves the scenario works. It does not prove it is worth
doing. Hand every new or materially changed scenario to the
[scenario-review](../scenario-review/SKILL.md) skill, which activates it on a
live lab, walks it as a platform engineer, tamper-tests every check, scores it
out of 5 and improves it until it reaches 4.8.

```
/scenario-review my-scenario
```

Treat the score as part of the definition of done: below 4.8 the scenario is a
draft, whatever `validate` says.

## Before you finish

- [ ] `labctl validate` is clean.
- [ ] Every `helm` component has a `version`, pinned in `config/versions.env`.
- [ ] No duplicated platform values; `platformValues:` used where applicable.
- [ ] Every `script` component has an `uninstallScript`.
- [ ] Checks assert the observable outcome, with `remediation` where useful.
- [ ] Added to the catalog in [docs/scenarios.md](../../../docs/scenarios.md).
- [ ] New or changed schema fields documented in
      [the schema reference](../../../docs/reference/scenario-schema.md).
- [ ] Every objective is work the learner performs, not something a stage
      installed for them.
- [ ] Every metric the description promises is plotted on a dashboard the
      scenario links, and a `promql` check asserts it exists.
- [ ] Every check was sabotage-tested and went red.
- [ ] `scenario-review` scores it 4.8 or better.
- [ ] `make docs-check` passes.
