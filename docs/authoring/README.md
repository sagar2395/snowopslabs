# Authoring Guide

How to create content for SnowOps Labs — scenarios, incidents, learning paths,
platform modules — and the stability guarantees you can rely on.

> New here? Start with [Your First Scenario](first-scenario.md) — `labctl
> scenario new` scaffolds valid, verify-ready content in seconds.

## Contents

- [Your First Scenario](first-scenario.md) — the fast path: scaffold → edit →
  validate → verify → share.
- [Extension Seams](extensions.md) — the resolver and hook interfaces that let
  custom builds plug in without forking the engine.
- [SDK & Schema Stability Policy](sdk-stability-policy.md) — what we keep stable
  and how the schema versions.
- JSON Schema (for editor validation): the per-kind schemas under
  [`sdk/schemas/`](../../sdk/schemas/) — `scenario`, `incident`, `path`,
  `challenge`, and the shared `check`.
- Validate all content with `labctl validate` (checks schema, cross-references
  and templates; exits non-zero with `file:line: [kind/name] message`). See
  [R03 runbook](../runbooks/R03-content-authoring-and-validation.md) and
  [ADR-0009](../adr/0009-content-validation-strategy.md).
- Scenario format reference: [../scenarios.md](../scenarios.md)
- Platform module contract: [../../platform/README.md](../../platform/README.md)

## Quick orientation

- A **scenario** is declarative YAML (`scenario.yaml`) + assets (manifests,
  values, dashboards, scripts). It declares `objectives`, `stages` of
  `components`, and machine-verifiable `checks`. Set
  `apiVersion: scenario.snowops.net/v2`.
- An **incident** is a small directory (`fault.yaml`, `inject.sh`,
  `resolve.sh`, `hints.md`) declaring a reversible fault and the check that
  detects it.
- A **platform provider** is `platform/<category>/<provider>/` with
  `install.sh`, `uninstall.sh`, `status.sh` and `values.yaml`.
- Content is **cross-platform, idempotent and declarative** — never hardcode
  logic in Go (see [CONTRIBUTING.md](../../CONTRIBUTING.md) golden rules).

## Distribution

There is no pack format or registry. Content lives in directories, so
publishing is `git push` and consuming is `git clone` plus
`SNOWOPS_CONTENT_PATH`. The reasoning is in
[ADR-0008](../adr/0008-content-extensibility-seam.md).

## Validate before you PR

```bash
bin/labctl validate                  # schema + cross-reference integrity (CI gate)
bin/labctl scenario info <name>      # parses + renders stages/checks
bin/labctl scenario verify <name>    # runs the checks against a live cluster
```
