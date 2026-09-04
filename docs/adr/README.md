# Architecture Decision Records

Each ADR records one decision, the alternatives considered, and why the
alternative was rejected. ADRs are immutable once accepted — a later decision
supersedes an earlier one with a new record rather than editing it.

Write a new ADR whenever a task makes a choice that a future maintainer would
otherwise have to reverse-engineer from the code.

| # | Decision | Status |
|---|---|---|
| [0001](0001-cut-cloud-and-commercial-scope.md) | Cut cloud runtimes and commercial surfaces | Accepted |
| [0002](0002-sqlite-persistence.md) | SQLite (pure Go) for persistence | Accepted |
| [0003](0003-durable-run-engine.md) | Durable run engine over scripts, not a K8s operator | Accepted |
| [0004](0004-lock-and-reject-concurrency.md) | Exclusive lock keys, reject conflicting runs | Accepted |
| [0005](0005-module-root-and-layering.md) | Module at repo root; service-layer architecture | Accepted |
| [0006](0006-api-conventions.md) | `/api/v2`, problem+json, cursor streaming | Accepted |
| [0007](0007-ui-stack.md) | React Router + TanStack Query + Tailwind + Radix | Accepted |
| [0008](0008-content-extensibility-seam.md) | Content paths and extension seam instead of a marketplace | Accepted |
| [0009](0009-content-validation-strategy.md) | JSON Schemas for authoring, Go loaders for validation | Accepted |
| [0010](0010-platform-values-single-source.md) | Platform values are the single source of truth; scenarios overlay or adopt | Accepted |
| [0011](0011-chart-pinning-and-repo-migration.md) | Pin every chart; migrate off the deprecated Grafana charts | Accepted |
| [0012](0012-alloy-as-trace-collector.md) | Grafana Alloy as the trace collector; Promtail retained for logs | Accepted |

Template: `0000-template.md`.
