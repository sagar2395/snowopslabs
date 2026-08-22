# ADR 0002 — SQLite (pure Go) for persistence

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W1

## Context

v1 kept run state in an in-memory map capped at 100 entries and results in JSON
files under `.labctl/`. Consequences: run history vanished on restart, concurrent
writes to the JSON files were unsynchronised, and there was no way to query
anything ("show me every failed check for this scenario").

v2 needs durable runs, replayable logs, component inventory, sessions and an
audit trail — with transactional integrity, since a half-written component
record makes teardown wrong.

## Decision

Use **SQLite through `modernc.org/sqlite`**, a pure-Go implementation.

Database at `~/.snowops/snowops.db` (override via `SNOWOPS_HOME`).
Schema managed by embedded, forward-only migrations applied on open. A database
whose schema version exceeds what the binary knows is a hard error, not a
best-effort read.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep JSON files | No transactions, no queries, no concurrent-write safety; the problem we are fixing |
| `mattn/go-sqlite3` (cgo) | cgo breaks cross-compilation and static binaries, violating the cross-platform golden rule. Non-negotiable. |
| Embedded KV (bbolt, badger) | No ad-hoc queries or joins; we would hand-roll indexing for leaderboards and result filtering |
| Postgres | Requires a server the laptop-first user does not have. Reconsider only if multi-tenant hosting becomes a goal |

## Consequences

- **Easier:** real queries for results, leaderboards and audit; atomic writes;
  a single file to back up or mount on a PVC in server mode.
- **Harder:** writes serialise. Acceptable — this is a single-cluster tool, not
  an OLTP system. WAL mode and a bounded connection pool keep it comfortable.
- **Testing:** tests open a database in `t.TempDir()`, so every store test is
  hermetic and parallel-safe.
- Log retention needs a policy; unbounded `run_logs` growth is a real risk on a
  long-lived server. Retention/pruning ships in W1.
- **The driver sets the minimum Go version.** `modernc.org/sqlite` is a
  transpiled C library and tracks new Go releases closely: its recent versions
  require Go 1.25. We pin `v1.38.0`, the newest release that still builds on Go
  1.24, so contributors are not forced onto a toolchain released weeks ago.
  Revisit when Go 1.25 is widely available in distributions and CI images.
