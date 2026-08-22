# Testing Strategy (v2)

> All four layers below are **mandatory**, not aspirational. A task is not done
> until every applicable layer covers it. See the Definition of Done in
> `docs/ROADMAP.md`.
> Last updated: 2026-07-26

---

## Why this is strict

v1 had 25k lines of Go with reasonable unit coverage, **128 shell scripts with
zero tests**, and **a UI with zero tests**. The untested surfaces were exactly
the ones that touch real clusters and real users. v2 closes both holes and keeps
them closed with CI gates.

## The four layers

### Layer 1 — Go unit tests

**Scope:** every package under `internal/` and `pkg/`.

Requirements:

- **Table-driven** whenever two or more cases share a shape. Never
  `TestFoo_CaseA` + `TestFoo_CaseB` as separate top-level functions for the same
  behaviour.
- **Per exported function:** the happy path, at least two error/edge cases, and
  — wherever the function accepts a `context.Context` — a cancellation test and
  a deadline-exceeded test.
- **Hermetic, always.** `t.TempDir()` for disk. `toolchain.Fake` for every
  external binary. An in-memory or temp-file SQLite for the store. No test may
  touch a live cluster, the network, or real credentials.
- **Race detector.** All tests run under `-race` in CI.
- **Coverage ≥ 80% statements** per package. CI fails below the line.

Edge cases that must be covered wherever they apply, because these are where
real systems break:

| Class | Examples |
|---|---|
| Cancellation | ctx cancelled before start, mid-execution, during cleanup |
| Timeouts | deadline exceeded, timeout during a child process |
| Concurrency | two runs on the same lock key, N concurrent readers, shutdown mid-write |
| Partial failure | script fails at step 3 of 7; resume converges |
| Malformed input | truncated YAML, wrong types, unknown fields, cyclic references |
| Boundary | empty list, single item, 10k items, unicode names, very long output lines |
| Filesystem | missing file, permission denied, symlink escaping the content root, disk full |
| Clock | operations spanning a DST change; monotonic vs wall time for durations |

### Layer 2 — Shell tests (bats)

**Scope:** every `.sh` file containing branching logic — that is,
`platform/*/*/{install,uninstall,status}.sh`, `runtimes/*/*.sh`,
`incidents/*/{inject,resolve}.sh`, `bootstrap/`, and scenario hooks.

Mechanism: `kubectl`, `helm`, `k3d` and `kind` are replaced by **stubs on
`PATH`** that record their argv to a log file and return scripted exit codes.
Tests then assert on the recorded invocations.

Every script test asserts at minimum:

- **Idempotency** — running twice produces the same end state and the second run
  makes no destructive call.
- **Failure propagation** — a stubbed tool failing non-zero causes the script to
  exit non-zero (proving `set -euo pipefail` is doing its job).
- **Portability** — no GNU-only flags. Enforced additionally by `shellcheck` and
  a grep gate for `grep -oP`, `sed -i` without a backup suffix, `readlink -f`,
  `date -d`.
- **Environment contract** — the script reads `${VAR:-default}` from the
  executor's environment and does *not* source `.env` itself.

### Layer 3 — API contract & CLI integration tests

**Scope:** every HTTP endpoint and every CLI command.

- **HTTP:** `httptest` server wired to real services backed by a temp SQLite and
  `toolchain.Fake`. Each endpoint asserts the success shape **and** its
  `problem+json` error envelope, including status code and stable `type` slug.
- **Streaming:** tests force a disconnect mid-stream and assert cursor resume
  produces no gaps and no duplicates.
- **Concurrency:** tests assert a conflicting run is rejected with 409 naming
  the lock holder.
- **CLI:** golden-file tests for human-readable output plus schema assertions
  for `--output json`. Golden files are regenerated with `make golden-update`
  and reviewed as part of the diff.
- **OpenAPI** is generated from the contract tests, so it cannot drift.

### Layer 4 — UI tests

- **Component (Vitest + Testing Library + MSW):** every component and hook.
  Renders, states (loading/empty/error/offline), user interaction, and
  accessibility roles. API calls mocked at the network layer with MSW, never by
  stubbing modules. ≥80% statement coverage.
- **E2E (Playwright):** critical journeys against the real binary with a fake
  toolchain — so it exercises actual routing, real HTTP and real streaming
  without a cluster:
  1. Log in (auth enabled) and land on Overview.
  2. Start a lab, watch the run console stream, cancel it, see `cancelled`.
  3. Activate a scenario, run verify, read per-check observed-vs-expected.
  4. Inject an incident, consume a hint, resolve it, see MTTR recorded.
  5. Start a learning path, complete a module, see progress persist.
  6. Force a disconnect mid-stream, assert log continuity on reconnect.
  7. Deep-link straight to a nested URL and refresh — state intact.
- **Accessibility:** `axe` scan on every route; zero serious/critical violations.
- **Visual:** Playwright screenshot snapshots on the design-system page, both
  themes, to catch unintended regressions.

## The nightly real-cluster e2e

Layers 1–4 are hermetic and run on every PR in minutes. Reality is checked once
a night on `kind`:

- Provision a kind cluster in CI.
- Install the verified platform components.
- For **every scenario marked `verified`**: activate → verify → tear down.
- For **every incident marked `verified`**: inject → assert its detection check
  fires → resolve → assert resolution.
- Assert no leaked namespaces, PVs or processes at the end.

A content item may only carry the `verified` badge if it passes this job. This
is what makes the badge meaningful, and it is why the roadmap curates a small
verified set rather than claiming all thirteen scenarios work.

## Static analysis gates

Every gate fails the build, none are advisory:

| Gate | Tool |
|---|---|
| Go lint | `golangci-lint` (errcheck, govet, staticcheck, revive, bodyclose, contextcheck, errorlint) |
| Go security | `gosec` |
| Dependency CVEs | `govulncheck` |
| Shell lint | `shellcheck` (all files) + `shfmt -d` |
| Portability | grep gate for GNU-only constructs |
| TypeScript | `tsc --noEmit` with `strict: true` |
| JS lint | `eslint` including `jsx-a11y` |
| Licence | dependency licence scan |
| Formatting | `gofmt -l` must be empty; `prettier --check` |

## Fuzzing

Go native fuzz targets, with a checked-in corpus, on every parser that consumes
untrusted or authored input: scenario/incident/path/challenge YAML, check
expressions, step markers in script output, template resolution, and API request
bodies. Fuzz targets run briefly on PRs and for an extended period nightly.

## Make targets

```bash
make test              # layers 1-3, race detector, coverage gate
make test-go           # layer 1
make test-shell        # layer 2 (bats)
make test-api          # layer 3
make test-ui           # layer 4 component (vitest)
make test-e2e          # layer 4 journeys (playwright, fake toolchain)
make test-cluster      # nightly real-cluster e2e on kind
make test-coverage     # HTML coverage report
make lint              # every static analysis gate
make golden-update     # regenerate CLI golden files
```
