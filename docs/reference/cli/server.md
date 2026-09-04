# Server, metrics & auth

## `labctl ui`

Serves the web dashboard and the REST API, and opens a browser.

```bash
labctl ui                             # http://127.0.0.1:3939
labctl ui --port 8080
labctl ui --ui-dir src/ui/dist        # serve the UI live from disk during development
```

| Flag | Default | Description |
|---|---|---|
| `--port` | `3939` | port to serve on |
| `--bind` | `127.0.0.1` | address to bind. `0.0.0.0` exposes it on the network and requires auth |
| `--ui-dir` | `$LABCTL_UI_DIR` | serve the UI from this directory instead of the embedded bundle |
| `--tls-cert` | — | TLS certificate (PEM); enables HTTPS when set with `--tls-key` |
| `--tls-key` | — | TLS private key (PEM) |

The server refuses a network-exposed bind while authentication is off, because
that would put cluster-control endpoints on the network.

The dashboard shows cluster status, platform component health, applications
with deploy and destroy actions, scenarios with activate and deactivate
controls, and live updates over WebSocket. The UI is built into the binary by
`make cli-build`; during development, `make ui-dev` serves it from disk with no
Go rebuild.

## The API

Everything the UI does is available under **`/api/v2`** — the only API surface.
There is no unversioned `/api`; a request there gets the SPA shell, not JSON
([ADR-0013](../../adr/0013-remove-unversioned-api.md)).

Conventions, from [ADR-0006](../../adr/0006-api-conventions.md):

- **Errors are `application/problem+json`.** Branch on the `type` slug, never on
  the message. The human-readable cause is in `detail`.
- **List collections are paginated**: `{items, nextCursor}`, with `?limit` (max
  200) and an opaque `?cursor`. Page until `nextCursor` comes back empty.
- **Catalog reads carry an `ETag`** and honour `If-None-Match` (→ 304).
- **Every request carries `X-Request-ID`**, echoed back and present on every log
  line — a sane inbound value is honoured, otherwise one is minted.
- **Events stream** over WebSocket at `/api/v2/ws`, with an SSE fallback at
  `/api/v2/stream`. Both are cursor-based: pass `?after=<seq>` to resume without
  a gap.

```bash
curl -s localhost:3939/api/v2/scenarios | jq '.items[].name'
curl -s 'localhost:3939/api/v2/runs?limit=10' | jq '.nextCursor'
```

## Metrics

An optional Prometheus endpoint, **off by default**:

```bash
LABCTL_METRICS=true labctl ui --port 3939
curl http://localhost:3939/metrics
```

When unset it adds no scrape surface at all. When enabled it serves the standard
text format with no auth, so a local Prometheus can scrape it.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `labctl_http_requests_total` | counter | `method`, `route`, `status` | API requests handled; `route` is the path template, e.g. `/api/v2/apps/{name}/build` |
| `labctl_http_request_duration_seconds` | histogram | `method`, `route` | API request latency |
| `labctl_runs_total` | counter | `kind`, `status` | run-engine runs by kind and terminal outcome |
| `labctl_run_duration_seconds` | histogram | `kind` | run-engine execution time |
| `labctl_runs_in_flight` | gauge | — | runs currently executing |
| `labctl_build_info` | gauge | `version` | always 1; carries the build version |

Metrics and access logs are installed as middleware around the whole mux, so a
new route cannot ship unmeasured.

## `labctl users` — team mode

Accounts for the API and UI. Authentication is **off by default**; the server
enforces it only when started with `LABCTL_AUTH=true`. These commands edit
`.labctl/users.yaml` (Argon2id hashes, mode 0600) either way. Older
PBKDF2-HMAC-SHA256 hashes still verify, so an existing file keeps working.

Two roles: `operator` (full control) and `participant` (run challenges,
incidents and learning paths, read status; cannot mutate platform, runtime, lab,
apps or services).

```bash
labctl users add alice --role operator --password 's3cret'
labctl users add bob   --role participant                      # prompts for the password
LABCTL_PASSWORD='pw' labctl users add carol --role participant # scripted
labctl users list                                              # names and roles, never hashes
labctl users remove bob
```

Password precedence for `users add`: `--password`, then `LABCTL_PASSWORD`, then
an stdin prompt.

Enable auth:

```bash
LABCTL_AUTH=true labctl ui --port 3939
```

The UI then shows a login screen, and the API requires a session cookie or
`Authorization: Bearer <token>` from `POST /api/v2/auth/login`. Participants get
**403** on operator-only mutations, and scored runs are attributed to the
authenticated user.

> OIDC and SSO are out of scope. Sessions are held in memory, so restarting the
> server logs everyone out. Serve behind TLS for anything beyond localhost — the
> cookie is `HttpOnly` and `SameSite=Strict`, and `Secure` only when the request
> arrived over TLS. See the
> [security posture](../../architecture/ARCHITECTURE.md#security-posture) for
> what is and is not implemented.
