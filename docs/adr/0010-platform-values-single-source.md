# ADR 0010 — Platform values are the single source of truth for a component

**Status:** Accepted
**Date:** 2026-09-04
**Wave:** W4

## Context

A platform component (`platform/logging/loki`, `platform/tracing/tempo`, …) ships
a `values.yaml`. Scenarios that need the same component were carrying their own
full copy of those values under `scenarios/<name>/values/`.

The two copies drifted. `scenarios/observability-sre/values/loki.yaml` set
`persistence.enabled` without a `storageClass`; the platform's set
`storageClass: local-path`. Whichever ran second issued a `helm upgrade` that
tried to change the StatefulSet's `volumeClaimTemplates`, and Kubernetes refuses
that:

```
UPGRADE FAILED: server-side apply failed for object monitoring/loki
apps/v1, Kind=StatefulSet: spec: Forbidden: updates to statefulset spec for
fields other than 'replicas', 'ordinals', 'template', 'updateStrategy',
'revisionHistoryLimit', 'persistentVolumeClaimRetentionPolicy' and
'minReadySeconds' are forbidden
```

The scenario worked on a clean cluster and failed on one where the platform
component was already installed — the more common case, and the harder one to
debug, because the error names Kubernetes' immutability rule rather than the
duplicated file that caused it. Grafana and Prometheus never hit this: one is a
Deployment, the other is managed by an operator.

## Decision

A component's values live in exactly one file, under `platform/`.

1. A scenario references it with `platformValues: logging/loki` (the component's
   `values.yaml`) or `platformValues: logging/loki/promtail-values.yaml` (a
   specific file), and supplies only its differences in `valuesFile`. Helm
   applies them in that order, so the scenario overlay wins on conflicts.
2. A scenario component may set `adopt: true`. When the release already exists,
   the scenario logs that it is adopting it and skips the upgrade entirely,
   rather than rewriting a release the platform owns.
3. As a backstop for clusters already in the broken state, `installHelm`
   recognises the immutable-StatefulSet error, deletes the StatefulSet with
   `--cascade=orphan` (pods and PVCs keep running) and retries the upgrade once.
   Any other failure surfaces unchanged.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep the copies, keep them in sync by review | Drift is invisible until an upgrade over an existing release fails, which is exactly when a learner is least able to diagnose it. |
| Always `helm uninstall` before a scenario installs | Destroys platform state a learner may share across scenarios, and deletes the PVC holding their logs. |
| Let scenarios reference the platform file with `../../platform/...` | The schema rejects `..` in content paths on purpose. `platformValues` is explicit about what is being referenced and cannot escape the tree. |
| Always adopt, never upgrade | A scenario that genuinely needs an overlay (Tempo's metrics generator) could not apply it. |

## Consequences

- One place to change a component's configuration; a scenario overlay states
  only what is different, which also documents the difference.
- `labctl scenario up` is idempotent over an existing platform install.
- Scenarios can no longer silently reconfigure a platform component. Anything
  they need beyond the platform default must be written in the overlay, in the
  open.
- The `--cascade=orphan` retry is a repair path, not a design: it only triggers
  on the specific immutable-field error, and logs what it is doing and why.
