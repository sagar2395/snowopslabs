# ADR 0005 — Module at the repo root; service-layer architecture

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W0

> **Update (issue #7 — repo restructure):** the Go module moved from the repo
> root into `src/` so the root reads as a content/authoring workspace and a
> later "the engine gets its own repo" split is a straight lift of `src/`. The
> **module path is unchanged** (`github.com/sagar2395/snowopslabs`), so every
> import path in this ADR still holds — only the on-disk location gained a `src/`
> prefix (e.g. `src/internal/service`). Tooling still runs from the module root;
> that root is now `src/`, and the top-level `Makefile` delegates into it. The
> service-layer decision below is otherwise unaffected.

## Context

v1's `go.mod` sat at `cmd/labctl/`, making the module path
`go.snowops.net/labctl` with public packages at
`go.snowops.net/labctl/pkg/checks`. Tooling (coverage, lint, vulnerability
scanning) had to be run from a subdirectory, and the layout confused every
convention that assumes the module root is the repo root.

Separately, business logic was spread across Cobra command functions and HTTP
handlers. `internal/api/handlers.go` was 695 lines mixing decoding, orchestration
and formatting, which is why its coverage sat at 42% while pure packages sat
above 85% — the logic simply was not reachable without an HTTP request.

## Decision

1. Move `go.mod` to the repo root, module `github.com/sagar2395/snowopslabs`.
2. Introduce `internal/service` as the only home for use-cases and invariants.
   `internal/cli` and `internal/httpapi` become thin adapters: parse input, call
   a service method, format output. Neither contains a business rule.
3. Every service method takes a `context.Context` as its first parameter.

`apps/go-api` keeps its own module — it is sample workload code, not part of the
tool.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Leave the module where it is | Every tool invocation needs a `cd`; the import path misleads readers about the repo's shape |
| Keep logic in handlers, test via HTTP only | Slow tests, poor coverage of edge cases, and a second copy of every rule in the CLI path |
| Full hexagonal/ports-and-adapters with interfaces everywhere | Over-abstracted for this size; interfaces get defined at the consumer where a fake is actually needed, not preemptively |

## Consequences

- **Easier:** one command runs everything from the root. CLI and API share
  exactly one implementation of each use-case, so they cannot drift.
  Service tests are fast and hermetic, which is how coverage targets get met.
- **Harder:** a large one-time move with a noisy diff. Done in W0 before any
  feature work, so nothing is in flight across it.
- Import paths change everywhere. Acceptable: no external consumers exist.
