# CLAUDE.md

**SnowOps Labs**: a Kubernetes platform-engineering simulator. `labctl` (Go
CLI) stands up a local production-shaped cluster, runs declarative scenarios,
injects reversible faults, and grades the fix. Go 1.25, built on the toolchain
pinned in `src/go.mod` + React 18/Vite/TS UI (embedded in the binary) + POSIX
shell/helm/kubectl for all cluster work.

## Read this first

**[docs/AGENT-CONTEXT.md](docs/AGENT-CONTEXT.md)** is the working contract: the
routing table telling you which single document answers your task, every
invariant, and the definition of done.

Documentation is the source of truth. Trust it instead of re-deriving facts from
the source - that is the main way tokens get burned here. When a doc and the
code disagree, fix the doc in the same change.

## Layout

- `src/`: the Go module (`github.com/sagar2395/snowopslabs`) and the UI. **Run
  `go` and `make` targets from here.**
  - `src/cmd/labctl/` entrypoint · `src/internal/` implementation ·
    `src/pkg/` exported SDK · `src/ui/` React app · `src/services/` in-cluster
    helpers such as the k6 traffic generator
- `engine/`, `make/`, `platform/`, `runtimes/`: shell and helm orchestration
  that Go invokes
- `scenarios/`, `incidents/`, `challenges/`, `learn/`, `apps/` - declarative
  YAML and scripts
- `config/versions.env`: tool minimums **and** every chart pin
- `docs/`: the source of truth

## Commands

```bash
make cli-build      # build bin/labctl with the UI embedded
make ui-dev         # serve src/ui/dist live, no Go rebuild
make test           # Go unit + apps + shell (bats), -race, coverage gates, test-check
make test-go        # Go unit + contract tests (>=80% per package, changed lines >=85%)
make test-ui        # vitest component tests, same two gates
make test-e2e       # playwright journeys
make test-check     # a change to code changed a test with it
make lint           # fmt-check + shell + Go + UI lint + sec + vuln
make fmt            # gofmt the tree
make docs-check     # documentation is in step with the code
```

From `src/ui/`: `npm run test`, `npm run test:e2e`, `npm run typecheck`.

## The four loops

`labctl init` (build) → `labctl scenario up <name>` (simulate) →
`labctl incident inject <name>` (break) → `labctl scenario verify <name>`
(measure). Commands: [CLI reference](docs/reference/cli/index.md).

Providers are swappable via `.env` or CLI vars: `INGRESS_PROVIDER`,
`METRICS_PROVIDER`, `LOGGING_PROVIDER`, `TRACING_PROVIDER`, with
`PROFILE=k3d|kind|incluster`. Per-app config is `apps/<name>/app.env`.

## Go skills

Load the `golang-how-to` skill (cc-skills-golang) before writing, reviewing or
refactoring any Go code, and follow the skills it routes to. Where a skill and
[GO-CONVENTIONS](docs/GO-CONVENTIONS.md) differ, GO-CONVENTIONS wins (e.g. error
text says what to do next).

## Sub-agents and models

Plan and judge in the main session. Send search, log reading and test runs to the `scout` agent (Haiku) and
scoped implementation, tests and docs to the `worker` agent (Sonnet): [.claude/agents](.claude/agents).

- Delegate when the output is large and the conclusion small, or when tasks are independent (spawn them
  in parallel, in one message). Do not delegate one edit or a few tool calls.
- The brief names the files, what done looks like and the report format. Two writers never share a file.
- Check the diff or the test result before calling the work done. Do not re-read what an agent summarised.
- When an agent fails, move up one tier once (Haiku to Sonnet, Sonnet to Opus), never straight to Opus.

## Hard rules

The full set is in [AGENT-CONTEXT](docs/AGENT-CONTEXT.md#invariants). The ones
most often broken:

- POSIX shell only; no cgo; no `grep -P`, `sed -i` without a backup, `readlink -f`
  or `date -d`.
- Go orchestrates, scripts do the work. Never `exec.Command(...).Run()` - go
  through `internal/run` with a context, timeout and lock key.
- Scenarios, incidents, checks and learning paths are YAML plus scripts, never
  hardcoded Go.
- One values file per component; every chart pinned; `/api/v2` only;
  instrumentation is middleware, never per-handler.
- Comments (Go, shell, TypeScript, YAML) say **what the code does and why it
  is needed**, in the present tense, under three lines. They never tell the
  story behind a change: no bug or incident that prompted it, no decision or
  alternative considered, no measurements or logs, no "used to" / "once" /
  "no longer", no task or ticket numbers. That belongs in the commit message.
- Docs, content, comments, commits, issues and PRs describe the project
  itself: the problem, the reproduction and what changes. Never mention outside
  work you were doing when you found it, or that a change is for:
  [WRITING](docs/WRITING.md#write-about-the-project-not-the-occasion).
- No em dashes anywhere: docs, content, comments, CLI and UI text. Use a comma,
  a colon, parentheses or a plain " - ". `make docs-check` enforces it.
- Everything a person reads (docs, READMEs, scenario and fault text, CLI and UI
  messages) follows [WRITING](docs/WRITING.md): plain, specific, written like
  an engineer talking to a colleague. No hype words, no filler, no opening or
  closing paragraphs that say nothing. `make docs-check` rejects the worst
  phrases.
- Every change ships its tests in the same commit, at the layer that owns the
  behaviour. [TESTING](docs/TESTING.md#what-each-change-needs) has the table of
  what each kind of change needs; `make test-check` and the changed-lines gate
  block a change that brings none.
- A bug fix starts with a test that fails because of that bug. A bug that only
  a live cluster can reproduce becomes a step in that content's review guide.
- A test asserts an outcome a user would notice. Break the behaviour once and
  watch the test fail before you trust it; "ran without error" is not a test.
- A check, grader or detection script has a test per lab state: solved
  passes; the work not done, each known wrong fix, and a cluster that cannot
  answer all fail.
- Tests are table-driven and hermetic. No live cluster, network or real
  credentials.
- No new coverage exceptions or exclusions. Floors only go up.
- Never land code without updating the docs it affects in the same change.
  The `docs-sync` skill has the table of what to update for each kind of
  change; use it at the end of every task that touched code, content or
  config. The website publishes these docs from the `stable` branch, so what
  you write here is what users read after the next release.
- Don't commit build artifacts (`bin/`, `dist/`, `coverage.out`, `src/ui/dist/`).

## Traps that cost a day each

Full detail lives with the subject; these are the pointers.

- **Observability**: kube-prometheus-stack's empty selectors, Tempo on port
  3200, Kafka's two metric sources, Promtail relabelling, k6's seconds-not-
  milliseconds: [R13](docs/runbooks/R13-observability-pipeline.md).
- **Helm**: `upgrade` and `uninstall` both ignore CRDs; most of a StatefulSet
  spec is immutable, so use `helm_upgrade_install` from `platform/_lib/helm.sh`:
  [R05](docs/runbooks/R05-platform-components.md).
- **Scenario authoring**: `platformValues` vs `valuesFile`, `adopt`,
  `uninstallScript`, `pending` checks:
  [scenario schema](docs/reference/scenario-schema.md).
