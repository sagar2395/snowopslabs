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
   only once — the user has really fixed it.

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

Then prove the escape hatch independently: inject, fix nothing, `resolve`, and
confirm the lab is clean. Also inject, fix it *partly* by hand, then `resolve`
— that is the path that breaks naive resolve scripts.

## Before you finish

- [ ] `fault.yaml`, `inject.sh`, `resolve.sh`, `hints.md`, `solution.md` all present.
- [ ] `inject.sh` is idempotent; `resolve.sh` survives a partial manual fix.
- [ ] Detection check fails while broken and passes when fixed.
- [ ] Scripts are POSIX and pass `make lint-shell`.
- [ ] `alerts/rule.yaml` present if `expectAlert` is set.
- [ ] Listed in the fault table in [incidents/README.md](../../../incidents/README.md).
- [ ] `make docs-check` passes.
