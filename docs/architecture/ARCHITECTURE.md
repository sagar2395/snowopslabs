# SnowOps Labs Architecture (v2)

> The target design. Decisions with alternatives considered live in
> `docs/adr/`. Product rationale: `docs/PRODUCT.md`. Delivery order:
> `docs/ROADMAP.md`.
> Last updated: 2026-07-26

---

## 1. The shape of the system

```
┌───────────────────────────────────────────────────────────────────────┐
│  INTERFACES                                                           │
│    labctl CLI (Cobra)        Web UI (React SPA)      REST + WS/SSE    │
└──────────────┬─────────────────────┬────────────────────┬─────────────┘
               │                     │                    │
               └──────────── all three call ──────────────┘
                                     ▼
┌───────────────────────────────────────────────────────────────────────┐
│  SERVICE LAYER  (internal/service)                                    │
│    lab · platform · scenario · incident · learn · challenge · results │
│    Owns use-cases and invariants. No transport types, no cobra,       │
│    no HTTP. Every method takes a context.Context.                     │
└──────────────┬──────────────────────────────────┬─────────────────────┘
               ▼                                  ▼
┌──────────────────────────────┐   ┌──────────────────────────────────┐
│  RUN ENGINE (internal/run)   │   │  CATALOG (internal/catalog)      │
│   queue · exclusive locks    │   │   loads + validates all YAML     │
│   context cancel · timeouts  │   │   schema + cross-ref integrity   │
│   process-group kill         │   │   immutable, hot-reloadable      │
│   durable logs w/ sequence   │   └──────────────────────────────────┘
│   step tracking · retries    │
└──────────────┬───────────────┘   ┌──────────────────────────────────┐
               │                   │  CHECK ENGINE (pkg/checks)       │
               │                   │   http · kubectl · promql · script│
               │                   │   retry/eventually · observed vs  │
               │                   │   expected · per-check timeout    │
               │                   └──────────────────────────────────┘
               ▼
┌──────────────────────────────┐   ┌──────────────────────────────────┐
│  STORE (internal/store)      │   │  TOOLCHAIN (internal/toolchain)  │
│   SQLite, pure Go, migrations│   │   bash · kubectl · helm · k3d    │
│   runs · logs · steps · labs │   │   version preflight · doctor     │
│   components · results ·     │   │   argv-only, never shell strings │
│   users · sessions · audit   │   │   Fake impls for hermetic tests  │
└──────────────────────────────┘   └──────────────┬───────────────────┘
                                                  ▼
                            ┌──────────────────────────────────────────┐
                            │  CONTENT (the actual work)               │
                            │   platform/<category>/<provider>/*.sh    │
                            │   scenarios/ incidents/ learn/ challenges│
                            └──────────────────────────────────────────┘
```

**The rule that keeps this honest:** Go orchestrates, records and grades. Shell
scripts and YAML do the domain work. `labctl` never reimplements `helm` or
`kubectl` logic. Adding a platform provider means adding a directory of
scripts, never touching Go.

## 2. Module layout

The v1 module root sat at `cmd/labctl/`, which made `pkg/` un-importable in the
idiomatic way and confused tooling. v2 gave the module a repo-root-derived path.
Issue #7 then moved the whole module under `src/` so the repository root reads as
a content/authoring workspace and a later "the engine gets its own repo" split is
a straight lift of `src/`. **The module path is unchanged**
(`github.com/sagar2395/snowopslabs`), so every import below is still written with
no `src/` prefix — only the on-disk path gained one:

```
src/go.mod                      module github.com/sagar2395/snowopslabs
src/cmd/labctl/main.go          entrypoint, nothing else
src/internal/cli/               cobra commands — thin, parse + call service
src/internal/httpapi/           REST/WS/SSE — thin, decode + call service
src/internal/service/           use-cases (the only place invariants live)
src/internal/run/               durable run engine
src/internal/store/             SQLite persistence + migrations
src/internal/catalog/           declarative content loading + validation
src/internal/toolchain/         external binary adapters + fakes
src/internal/config/            layered config resolution
src/pkg/checks/                 public: the check engine
src/pkg/scenario/               public: content types + schema
src/pkg/extension/              public: the third-party seam
src/ui/                         React SPA (built, embedded into the binary)
```

The user-facing content (`scenarios/`, `incidents/`, `apps/`, `platform/`,
`runtimes/`) stays at the repo root; `labctl` discovers it by walking up from the
working directory (it keys on `scenarios/` + `runtimes/`), so it runs unchanged
from the repo root even though the binary is built under `src/`.

`internal/cli` and `internal/httpapi` are deliberately dumb. If logic can be
tested without a terminal or an HTTP request, it belongs in `internal/service`.

## 3. The run engine

This is the heart of the v2 redesign and the single biggest departure from v1,
where `executor.RunScript` called `cmd.Run()` with no context, kept job state in
a 100-entry in-memory map, and dropped log lines for slow clients.

### 3.1 Lifecycle

```
  Submit(spec) ──▶ [queued] ──▶ [running] ──┬──▶ [succeeded]
                      │                     ├──▶ [failed]      (non-zero exit)
                      │                     ├──▶ [cancelled]   (user or shutdown)
                      │                     └──▶ [timed_out]
                      └──▶ [rejected]  (lock conflict — 409, names the holder)
```

A `RunSpec` declares: kind (`platform.install`, `scenario.activate`,
`incident.inject`, …), target, script path, argv, environment, timeout, and a
**lock key**.

### 3.2 Cancellation, properly

Every run gets a `context.Context`. Cancellation is not best-effort:

1. The process is started in its **own process group** (`Setpgid`), so children
   — and `helm` spawns plenty — are reachable.
2. On cancel, `SIGTERM` goes to the whole group.
3. After a grace period (default 15s, configurable per run kind), `SIGKILL`.
4. The run is marked `cancelled` with the partial log intact, and any registered
   compensating action runs.

Server shutdown cancels all in-flight runs the same way and waits for them,
rather than leaving orphaned `helm` processes behind.

### 3.3 Concurrency and locking

Runs declare a lock key, typically `lab:<name>` or `lab:<name>/ns:<namespace>`.
The engine holds an exclusive lock per key. A conflicting submission is
**rejected immediately** with the ID and description of the run holding the
lock — not silently queued, because a user who fires `platform up` twice wants
to be told, not to wait five minutes for a duplicate.

Runs with disjoint lock keys execute concurrently, bounded by a worker pool.

### 3.4 Durable, replayable logs

Every output line is persisted as `(run_id, seq, stream, ts, text)` with `seq`
strictly monotonic per run. Consumers stream **from a cursor**:

- WebSocket client connects with `?after=<seq>` → gets everything it missed,
  then live output. A reload or a dropped connection loses nothing.
- Slow consumers apply backpressure to their own delivery only. The write path
  to SQLite is never skipped, so **no line is ever dropped**.
- Logs are readable after the run finishes, after a restart, forever (subject to
  retention).

### 3.5 Steps

Long scripts emit step markers (`##snowops:step:<name>`) that the engine
parses into structured step records. That gives the UI a real progress timeline
("Installing Prometheus… 3 of 7") rather than an opaque wall of text, and gives
failure reports a precise "failed at step X".

## 4. Persistence

**SQLite via `modernc.org/sqlite`** — a pure-Go implementation, no cgo. That
matters: cgo would break cross-compilation and the cross-platform golden rule.
See `docs/adr/0002-sqlite-persistence.md`.

Database lives at `~/.snowops/snowops.db` (override with
`SNOWOPS_HOME`). Schema is versioned with embedded, forward-only migrations
applied on open; a newer schema than the binary understands is a hard, clear
error rather than a corruption risk.

Core tables:

| Table | Holds |
|---|---|
| `runs` | id, kind, target, status, lock key, actor, timings, exit code, error |
| `run_logs` | per-run ordered output lines (the replay source) |
| `run_steps` | parsed step timeline |
| `labs` | lab identity, runtime profile, lifecycle state |
| `components` | what is installed where — makes `status` fast and `down` precise |
| `scenario_state` | active scenario, current stage, activation history |
| `incidents` | injected faults, detect/resolve timestamps, hints consumed |
| `results` | graded outcomes for scenarios, incidents, challenges, paths |
| `users`, `sessions` | auth (server mode) |
| `audit` | who did what, when, from where |

`components` is the pragmatic middle ground between v1's fire-and-forget and a
full reconciler: we record what we installed so teardown is exact and status is
instant, without claiming to own drift correction.

## 5. Toolchain adapters

All external binaries go through `internal/toolchain`. Each adapter:

- **Preflights** on first use: binary present, version at or above minimum,
  actionable error otherwise ("helm 3.9.0 found, 3.12+ required — brew upgrade helm").
- **Builds argv arrays, never shell strings.** No user-supplied value is ever
  interpolated into a command line. Script paths resolve against an allowlisted
  root with symlink-resolved containment checks, closing the path-traversal hole.
- **Has a fake.** `toolchain.Fake` records invocations and returns scripted
  responses, which is what lets service-layer and API tests be fully hermetic —
  no cluster, no network, in any test.

`labctl doctor` runs every preflight at once and prints a fix for each failure.

## 6. Content model

All content is YAML, loaded and validated by `internal/catalog` at startup:

1. **Schema validation** against JSON Schema (`sdk/schemas/`), which also gives
   editor autocomplete via `.vscode/settings.json`.
2. **Cross-reference integrity**: a learning path referencing a missing
   scenario, a challenge referencing a missing incident, or a check referencing
   an undefined namespace fails at load, not at 3am mid-demo.
3. **Template safety**: `{{.DomainSuffix}}` and friends resolve from typed
   config; an unknown key is an error, not an empty string.

`labctl validate` runs the whole thing and is a required CI gate. Content is
immutable once loaded; a file watcher triggers a full atomic reload in dev.

### Check engine v2

Checks stay the grading primitive. v2 adds what real assertions need:

- **`eventually` semantics**: retry with backoff until a deadline, because
  "readyReplicas >= 1" is true *eventually*, not instantly.
- **Observed vs expected in the result**, so a failure says
  `p99 latency: observed 0.84s, expected < 0.30s` instead of `FAIL`.
- **Per-check timeout and independence** — one hanging check cannot stall a
  verify run.

## 7. API

Versioned under `/api/v2`. Conventions applied uniformly:

- **Errors are `application/problem+json`** (RFC 7807) with a stable `type`
  slug, so clients branch on a constant, not a message string.
- **Mutations return `202` with a run ID**; progress is streamed, never polled
  blindly.
- **Streaming is WS with an SSE fallback**, both cursor-based (§3.4).
- **Collections paginate** with cursors and return `ETag`; catalog reads honour
  `If-None-Match`.
- Every request carries a request ID, echoed in the response and every log line.

### Security posture

Server mode is opt-in but hardened by default:

- Binds `127.0.0.1` unless explicitly told otherwise; binding `0.0.0.0` without
  auth enabled is refused, not warned about.
- Sessions in SQLite (not an in-memory map that forgets everyone on restart),
  cookies `HttpOnly`/`SameSite=Strict`/`Secure` when served over TLS.
- CSRF tokens on mutating requests; rate limiting and constant-time comparison
  on the auth path.
- Argon2id password hashing (replacing v1's PBKDF2).
- Full audit trail of mutating operations.

## 8. Web UI

A full rebuild. v1 was a hand-rolled SPA with tab state in `useState` — no
routing, no deep links, no browser back, no caching, and zero tests.

**Stack:** React 18 + TypeScript (strict) + Vite, React Router (real URLs),
TanStack Query (caching, retry, invalidation), Tailwind with design tokens,
Radix primitives for accessible dialogs/menus/tabs.

**Principles:**

- **Deep-linkable.** `/labs/dev/scenarios/observability-sre/verify` is a URL you
  can send a colleague.
- **Descriptive over terse.** Every scenario, incident and check explains what
  it does, why it matters, and what "good" looks like *before* you run it.
- **The run console is a first-class view**, not a log dump: step timeline,
  live output, cancel button, failure summary pinned to the top.
- **Progress is visible.** Path completion, challenge scores, MTTR trends and
  check pass-rates are charted, not tabulated.
- **Accessible.** Keyboard navigable, screen-reader labelled, WCAG AA contrast
  in both light and dark themes.
- **Honest states.** Every view has designed loading, empty, error and
  offline states. No spinner that hangs forever when the backend is gone.

The built SPA is embedded into the Go binary — one artifact, no separate deploy.

## 9. Extensibility

With the marketplace cut, extensibility is a small, documented seam:

- **Content is inherently extensible**: drop a directory into `scenarios/`,
  `incidents/`, `learn/`, or `platform/<category>/<provider>/` matching the
  documented contract and it is discovered. `labctl scenario new` scaffolds it.
- **`SNOWOPS_CONTENT_PATH`** adds external content roots, so a team can keep
  private scenarios in their own repo without forking.
- **`pkg/extension`** keeps a resolver-chain seam for out-of-tree behaviour, with
  a stability policy (`docs/authoring/sdk-stability-policy.md`).

Everything the deleted marketplace did — publish, discover, install — is
achievable with git and a content path, without 2,000 lines of code.

## 10. Observability of SnowOps Labs itself

- Structured `slog` throughout, with request ID and run ID on every line.
- Optional `/metrics` (Prometheus): run counts by kind and outcome, run
  duration, queue depth, lock contention, check pass rate.
- `labctl runs list` / `labctl runs logs <id>` for post-hoc debugging — the
  durable store makes support questions answerable.
