# ADR 0004 — Exclusive lock keys; reject conflicting runs

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W1

## Context

v1 had no concurrency control. Two `platform up` invocations — from the CLI and
from an impatient double-click in the UI — would run simultaneously against the
same cluster, racing on Helm releases and namespaces. Helm's own locking
produces confusing mid-operation errors rather than a clear refusal.

## Decision

Every run declares a **lock key**, typically `lab:<name>` or
`lab:<name>/ns:<namespace>`. The engine holds an exclusive lock per key.

A submission whose lock key is held is **rejected immediately** — HTTP 409, or a
non-zero CLI exit — with an error naming the run that holds the lock, what it is
doing, and how long it has been running. Runs with disjoint keys proceed
concurrently, bounded by a worker pool.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Queue conflicting runs | A user who fires `platform up` twice wants to be told, not to silently wait five minutes for a duplicate install |
| Global mutex (one run at a time) | Blocks legitimately independent work — watching traffic while inspecting a different namespace |
| Optimistic, let Helm arbitrate | Produces opaque failures deep in a run, after partial mutation. Worse than refusing up front |

## Consequences

- **Easier:** the failure mode is a clear, immediate message instead of a
  corrupted half-install. Trivially testable.
- **Harder:** callers must handle 409. The CLI prints the holder and suggests
  `labctl runs logs <id>`; the UI surfaces it as a non-blocking notice with a
  link to the running job.
- Lock keys must be chosen carefully. Too coarse serialises unnecessarily, too
  fine allows real races. The key for each run kind is declared in one table in
  `internal/run` and reviewed as part of any new run kind.
- Locks live in memory and are rebuilt at startup from `runs` in a non-terminal
  state, which are marked `cancelled` on recovery since their processes are gone.
