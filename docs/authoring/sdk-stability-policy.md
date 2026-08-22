# SDK & Schema Stability Policy

This policy defines what SnowOps Labs promises to keep stable so content authors
and downstream tools can build on it with confidence. It is the contract that
lets **content and engine evolve on independent clocks**.

## What is covered (the public surface)

- **The content schemas** — `scenario.yaml` and, from W2-T01, `fault.yaml`,
  `path.yaml` and `challenge.yaml`, each identified by `apiVersion`
  (e.g. `scenario.snowops.net/v2`) and published as a JSON Schema under
  [`sdk/schemas/`](../../sdk/schemas/).
- **The check types** — `http`, `kubectl`, `promql`, `script` and their fields.
- **The platform provider contract** — the four scripts and the environment
  variables that select a provider.
- **The public Go SDK** — packages under [`pkg/`](../../pkg/):
  `pkg/checks`, `pkg/scenario`, `pkg/extension`.

Anything under `internal/` is **not** public and may change at any time.

## Versioning rules

- **SemVer** for the engine/CLI.
- Content schemas are versioned by `apiVersion`. The engine supports the
  **current and previous** versions (N and N-1). A breaking schema change means
  a new `apiVersion` (e.g. `…/v3`), never a silent change to `v2`.
- A missing `apiVersion` is treated as the engine's current default.

## Compatibility promise

- Within an `apiVersion` we only **add** optional fields. We never remove or
  repurpose a field, nor tighten validation in a way that breaks valid content.
- Deprecations are announced in the changelog with a **minimum one-minor-version
  window** before removal, and removal only happens at a new `apiVersion`.
- Each release publishes a **compatibility matrix** stating which CLI supports
  which content `apiVersion`s (see [RELEASING.md](../../RELEASING.md)).

## How breaking changes happen

Any change to a covered surface requires an **RFC** under
[`docs/rfcs/`](../rfcs/) approved by the lead maintainer
([GOVERNANCE.md](../../GOVERNANCE.md)). The RFC states the new `apiVersion`, the
migration path, and the deprecation window.

## For content authors

- Pin the `apiVersion` in your content so it fails fast on an incompatible
  engine rather than misbehaving.
- Validate locally with `labctl validate`, and against the published JSON Schema
  in your editor.
