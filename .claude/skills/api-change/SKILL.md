---
name: api-change
description: "Add or change a SnowOps Labs HTTP API route, middleware, error shape, pagination or auth rule, and keep the React client in step. Use whenever a task touches src/internal/httpapi/, src/ui/src/api/, an /api/v2 endpoint, or the run/job streaming."
user-invocable: true
---

# Changing the API

## Read first

1. **[ADR-0006](../../../docs/adr/0006-api-conventions.md)** — the conventions
   every route obeys.
2. [docs/architecture/ARCHITECTURE.md §7](../../../docs/architecture/ARCHITECTURE.md)
   — the API's place in the system and the real security posture.
3. [docs/reference/cli/server.md](../../../docs/reference/cli/server.md) — what
   users are told the server does.

## The conventions

Users are served **`/api/v2` only**. There is no other version.

- **Errors are `application/problem+json`** with a stable `type` slug. Clients
  branch on a constant, never on a message string.
- **Mutations return `202` with a run ID.** Progress is streamed, not polled
  blindly.
- **Streaming is WebSocket with an SSE fallback**, both cursor-based.
- **Collections paginate** with an opaque cursor and return `{items, nextCursor}`
  plus an `ETag`; catalog reads honour `If-None-Match`.
- **Every request carries a request ID**, echoed in the response and on every
  log line.

## Instrumentation is middleware, never per handler

Metrics and access logging wrap the whole mux. Do **not** add a metrics call
inside a handler — that is exactly how `/` ended up serving all the traffic
unmeasured. A new route is measured because it is a route, not because someone
remembered.

Middleware order, outermost first: request ID → API version → access log →
CORS → JSON → auth → metrics. Request ID and version are set before anything can
reject the request, so even a CORS or auth failure gets a correlation ID and the
right error envelope. Metrics sit innermost so they time the handler itself.

## Adding a route

1. Register it in `registerAPI` in `src/internal/httpapi/server.go`, with its
   methods and `OPTIONS`.
2. Put the handler in the file for its resource — `lab.go`, `incident.go`,
   `challenge.go` and so on — not in `handlers.go` by default.
3. Return collections through `respondCatalog` so pagination and ETags are
   automatic. Return errors through `respondError` so the problem shape is.
4. Long-running work goes through `internal/run`, and the route returns the run
   ID. Never block a request on a shell-out.
5. Update the React client in `src/ui/src/api/client.ts` and its MSW handlers in
   `src/ui/src/test/server.ts`.

## Auth

The auth middleware is a pass-through when `LABCTL_AUTH` is off, so the local
experience is unchanged. When on, it gates every route except the auth
endpoints, and enforces operator-only mutations. A new mutating route is
operator-only unless there is a stated reason it is not — say so in the PR.

## Verify your work

```bash
make test-go                  # unit + contract tests, >=80% per package
make test-ui                  # vitest, including the API client
make lint
```

Tests are hermetic: `t.TempDir()`, `toolchain.Fake`, temp SQLite. Cover the
happy path and at least two error cases, and for any `context.Context` argument
add cancellation and deadline-exceeded tests.

## Before you finish

- [ ] Route registered once, under `/api/v2`.
- [ ] Errors are problem+json; collections paginate and carry an ETag.
- [ ] No per-handler metrics or logging.
- [ ] Mutations return a run ID rather than blocking.
- [ ] React client and MSW handlers updated.
- [ ] Endpoint documented in the relevant
      [CLI reference](../../../docs/reference/cli/index.md) page — every command
      page lists its REST equivalents.
- [ ] `make docs-check` passes.
