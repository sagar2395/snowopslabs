# Agent context

The entry point for any AI agent or new contributor working on SnowOps Labs.
Read this file, then read **only** the one or two documents the routing table
sends you to. Do not read the codebase to answer a question the docs answer.

---

## The contract

**Documentation is the source of truth.** It describes what the system does,
and the code is expected to match it. This is not aspiration — it is enforced
by `make docs-check` and by CI.

Three rules follow:

1. **Trust the docs.** If a document states a flag, a schema field, a path or a
   behaviour, act on it without opening the source to confirm. Reading code to
   re-derive documented facts is the main way agents burn tokens here.
2. **When docs and code disagree, the docs are the bug report.** Verify which
   side is right, fix the documentation in the same change, then fix the code.
   Never silently work around a wrong doc.
3. **A change is not done until the docs match it.** Same PR, not a follow-up.

Read code when — and only when — the docs are silent on what you need, the docs
contradict themselves, or you are editing the code in question.

---

## Where to look

| Your task | Read this | Don't read |
|---|---|---|
| Write or edit a scenario | [scenario schema](reference/scenario-schema.md), [first scenario](authoring/first-scenario.md) | `src/internal/scenario/` |
| Write or edit an incident | [incidents/README.md](../incidents/README.md), [R03](runbooks/R03-content-authoring-and-validation.md) | `src/internal/incident/` |
| Write a learning path or challenge | [learn/README.md](../learn/README.md), [challenges/README.md](../challenges/README.md) | `src/internal/learn/` |
| Add a platform component | [R05](runbooks/R05-platform-components.md), [ADR-0010](adr/0010-platform-values-single-source.md), [ADR-0011](adr/0011-chart-pinning-and-repo-migration.md) | `platform/*/` beyond your component |
| Add or change an API route | [ADR-0006](adr/0006-api-conventions.md), [architecture §7](architecture/ARCHITECTURE.md) | the whole `httpapi` package |
| Change the CLI surface | [CLI reference](reference/cli/index.md) | — |
| Fix a dashboard, metric or trace | [R13](runbooks/R13-observability-pipeline.md) | — |
| Understand the run engine | [architecture §3](architecture/ARCHITECTURE.md), [R01](runbooks/R01-run-engine-and-cancellation.md) | `src/internal/run/` |
| Write tests | [TESTING.md](TESTING.md) | — |
| Ship a release | [RELEASING.md](../RELEASING.md) | — |

Everything else: [docs/README index in the root README](../README.md#documentation).

## Skills

`.claude/skills/` holds packaged versions of the routes above, for harnesses
that support them — `scenario-author`, `incident-author`, `learning-author`,
`platform-component`, `api-change` and `docs-sync`. Each one names the documents
to read, the rules that are easy to break, and a checklist to finish against.
They are a shortcut to the docs, never a replacement: when a skill and a document
disagree, the document wins and the skill is the bug.

---

## Invariants

These hold everywhere. Breaking one is a review rejection, not a discussion.

**Shell and Go**

- POSIX shell only. No cgo. No `grep -P`, `sed -i` without a backup suffix,
  `readlink -f` or `date -d`. `make lint-shell` enforces this.
- Go orchestrates; scripts do the work. Never move helm/kubectl logic into Go.
- Everything that shells out goes through `internal/run` with a context, a
  timeout and a lock key. Never `exec.Command(...).Run()`.
- Every operation is idempotent. `helm upgrade --install`, `kubectl apply`;
  interrupting and re-running must converge.

**Content**

- Scenarios, incidents, checks and learning paths are YAML plus scripts. Never
  hardcoded Go.
- One values file per component, at `platform/<category>/<component>/values.yaml`.
  Scenarios point at it with `platformValues:` and supply only differences.
- Every chart is pinned in `config/versions.env`. No `helm upgrade --install`
  without `--version`.

**API and instrumentation**

- Users are served `/api/v2` only.
- Metrics and access logs are middleware wrapping the whole mux, never
  per-handler — so a new route cannot ship unmeasured.

**Tests**

- Table-driven and hermetic: `t.TempDir()`, `toolchain.Fake`, temp SQLite. No
  live cluster, no network, no real credentials.
- Happy path plus at least two error cases. Any `context.Context` argument gets
  cancellation and deadline-exceeded tests.

**Comments**

- Explain *why*, not *what*. Three lines is a lot; ten is always wrong.
- No task, ticket, wave or PR numbers in code comments. That history belongs in
  git and in ADRs.

---

## Definition of done

1. Code changed, `make lint` and `make test` pass.
2. Every doc the change touches is updated — schema, CLI reference, runbook.
3. A notable decision gets an ADR under `docs/adr/`.
4. `make docs-check` passes. It fails a change to `src/` or content with no
   matching documentation change, and catches doc paths the website publishes
   that no longer exist.
5. If you moved or renamed a published doc, update `scripts/docs-manifest.mjs`
   in the `snowopslabs-web` repository. The website reads these files directly;
   a rename with no manifest entry silently drops the page.
