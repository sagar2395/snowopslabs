# ADR 0006 — API v2: versioned, problem+json, cursor streaming

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W5

## Context

v1's API was unversioned, returned ad-hoc error shapes that varied by handler,
and streamed logs over a WebSocket that **dropped events for slow clients** —
the `default:` branch of a non-blocking channel send. A user who reloaded the
page or hit a brief network blip silently lost output, with no way to know.

## Decision

Adopt uniform conventions under `/api/v2`:

- **Errors are `application/problem+json`** (RFC 7807) with a stable machine
  `type` slug (`snowops.run.lock_conflict`), a human `title`, a `detail`, and
  the request ID. Clients branch on `type`, never on message text.
- **Mutations return `202 Accepted` with a run ID.** Progress arrives on the
  run stream.
- **Streaming is cursor-based.** `GET /api/v2/runs/{id}/stream?after=<seq>`
  over WebSocket, with an SSE fallback for constrained environments. Reconnect
  replays from the cursor: no gaps, no duplicates. Persistence is never skipped
  for a slow consumer — only that consumer's delivery is affected.
- **Collections paginate** with opaque cursors; catalog reads return `ETag` and
  honour `If-None-Match`.
- Every request gets a request ID, echoed in the response header and on every
  log line it produces.
- **OpenAPI is generated from the contract tests**, so the document cannot drift
  from the implementation.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep it unversioned | Guarantees a breaking change with no way to signal it |
| Custom `{error, message}` envelope | Works, but problem+json is a standard with existing client support and room for typed extensions |
| Long-poll instead of streaming | Higher latency and more load for a live log console |
| Hand-written OpenAPI | Drifts within weeks; the whole point is a contract that is true |

## Consequences

- **Easier:** the UI can implement one error handler and one reconnect strategy
  and have them work everywhere. Third-party scripting is realistic.
- **Harder:** every endpoint needs its `type` slug chosen and documented. The
  slug list lives in one file and is asserted stable by test.
- SSE fallback is a second streaming code path to maintain. Justified —
  WebSockets are still blocked by some corporate proxies.
