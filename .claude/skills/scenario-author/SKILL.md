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
  nothing about the pipeline.

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

## Before you finish

- [ ] `labctl validate` is clean.
- [ ] Every `helm` component has a `version`, pinned in `config/versions.env`.
- [ ] No duplicated platform values; `platformValues:` used where applicable.
- [ ] Every `script` component has an `uninstallScript`.
- [ ] Checks assert the observable outcome, with `remediation` where useful.
- [ ] Added to the catalog in [docs/scenarios.md](../../../docs/scenarios.md).
- [ ] New or changed schema fields documented in
      [the schema reference](../../../docs/reference/scenario-schema.md).
- [ ] `make docs-check` passes.
