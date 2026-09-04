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

## Implementation status

This ADR is the accepted target for all of Wave 5; it lands incrementally.

> The unversioned `/api` alias this ADR originally kept alongside `/api/v2` has
> since been removed — see [ADR-0013](0013-remove-unversioned-api.md).

- **Done (W5-T01):** the full handler set is mounted under `/api/v2`; every request
  carries a correlation ID — honoured from a sane inbound `X-Request-ID`, else a
  minted UUIDv4 — echoed in the `X-Request-ID` response header and attached to a
  structured `http_request` access-log line (method, route template, status,
  duration, size, client, request ID). See `internal/httpapi/middleware.go`.
- **Done (W5-T02):** `/api/v2` errors are `application/problem+json` with a
  stable `type` slug (`https://snowopslabs.dev/problems/<slug>`), `title`,
  `status`, `detail`, `instance` (request path) and the `requestId`. The closed slug set lives in
  `internal/httpapi/problems.go` and is asserted against actual usage by
  `TestProblemSlugs_StableSet`, so the error contract can't drift silently.
- **Done (W5-T03):** catalog reads carry a content-derived `ETag` and honour
  `If-None-Match` (→ 304); list collections return the opaque cursor envelope
  `{items, nextCursor}` (`?limit`, `?cursor`). See
  `internal/httpapi/httpcache.go` + `pagination.go`.
- **Done (W5-T04, streaming):** the event stream is cursor-based. Every event
  carries a monotonic `Seq`; the broadcaster keeps a bounded replay ring, and
  `SubscribeFrom(after)` returns the missed backlog plus the live channel
  atomically (gap-free, duplicate-free at the seam). WebSocket honours
  `?after=<seq>`; a Server-Sent Events fallback at `/stream` uses the same cursor
  (`?after` or the `Last-Event-ID` header) and writes `id:`-tagged frames so an
  `EventSource` resumes on its own. A cursor that has fallen off the ring gets a
  `resync` signal instead of a silent mid-stream start. See
  `internal/executor/broadcast.go` + `internal/httpapi/stream.go`.
- **Done (W5-T05, mostly):** passwords hash with **Argon2id** (legacy PBKDF2
  hashes still verify, so no rewrite is forced); the session cookie gains
  `Secure` automatically over TLS (`r.TLS` or `X-Forwarded-Proto: https`),
  keeping `HttpOnly` + `SameSite=Strict` always. **Deferred:** persisting
  sessions to SQLite so they survive a restart — low value for the local golden
  path, and it belongs with the team-server work (W8); tracked there.
- **Done (W5-T06, mostly):** login is **rate-limited** per client (fixed window,
  5/min, reset on success) — a credential-stuffing loop gets `429 + Retry-After`.
  Constant-time password comparison was already in place. **CSRF tokens
  deprioritized** — `SameSite=Strict` cookies plus the existing cross-origin
  POST/DELETE rejection already close the CSRF vector for this tool; explicit
  double-submit tokens would add client friction for little marginal safety.
- **Done (W5-T07):** the server **refuses a non-loopback bind without auth**
  (`labctl ui --bind 0.0.0.0` exits non-zero unless `LABCTL_AUTH=true`), the
  default bind is now `127.0.0.1`, and **TLS** is supported via `--tls-cert` /
  `--tls-key`. See `internal/httpapi/server.go` (`CheckBind`) and `cli/ui.go`.
- **Done (W5-T09):** optional Prometheus `/metrics`.
- **Still ahead:** audit log (T08), generated OpenAPI from the contract suite
  (T10). Mutations still return their current status codes rather than the
  `202 + run ID` shape — a later refinement on top of this stream.
