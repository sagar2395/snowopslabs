# Go conventions

How Go code is written in `src/`. Read this before changing anything under
`src/cmd/`, `src/internal/` or `src/pkg/`. The hard rules are in
[AGENT-CONTEXT](AGENT-CONTEXT.md#invariants) and the test rules are in
[TESTING](TESTING.md). This page covers the everyday choices those two leave
open, with an example from the codebase for each.

The language floor is the `go` directive in `src/go.mod` (1.25). Use anything
that version allows. The linter flags the older forms.

---

## 1. Where code goes

| Layer | Package | Owns | Must not |
|---|---|---|---|
| Entry point | `cmd/labctl` | calling `cli.Execute` | hold logic (`TestMainStaysTrivial`) |
| Transport | `internal/cli`, `internal/httpapi` | parsing input and rendering output | decide anything a test could check without a terminal or a request |
| Use-cases | `internal/service/*` | invariants, lock keys, run kinds | import cobra or `net/http` |
| Execution | `internal/run` | queueing, locks, cancellation, durable logs | know what a scenario or a fault is |
| Adapters | `internal/toolchain`, `internal/k8s`, `internal/store` | talking to binaries, the cluster, SQLite | carry domain rules |
| Public SDK | `pkg/*` | the stable surface that extensions build on | import `internal/` ([SDK policy](authoring/sdk-stability-policy.md)) |

Ask where the rule lives before you add code. If a CLI command and an HTTP
handler would both need it, it belongs in a service.

Some older paths still skip these layers. Their backlog items describe how to
retire them, and new code should not copy them:

- The web UI's mutating actions use `internal/executor` instead of the run
  engine: [B16](backlog.md#b16--web-ui-actions-bypass-the-run-engine).
- `internal/cli` keeps its wiring in package-level variables:
  [B18](backlog.md#b18--internalcli-runs-on-package-level-state).

## 2. Context

- A function that does I/O or waits on anything takes `ctx context.Context` as
  its first parameter. `revive`'s `context-as-argument` rule enforces the
  position.
- Pass the caller's context down. Only create `context.Background()` where the
  work must outlive its caller. A run outlives the HTTP request that submitted
  it, which is why `run.(*Engine).worker` starts from `Background` and says so
  in a comment. Write that comment whenever you do the same.
- Don't store a context in a struct. The two exceptions (`run.logSink` and
  `service/scenario`'s writer) are each scoped to a single run.
- In tests, use `t.Context()`. Don't write `context.WithCancel(context.Background())`
  followed by `defer cancel()`.

## 3. Errors

- Wrap with context and `%w`, in lowercase, with no trailing punctuation:
  `fmt.Errorf("creating run %s: %w", id, err)`.
- Use `errors.New` when there is nothing to format. `perfsprint` flags
  `fmt.Errorf` calls that have no arguments.
- A caller that needs to branch gets either a sentinel (`run.ErrQueueFull`,
  `incident.ErrNoActive`) or a typed error that carries the detail the message
  needs (`*run.LockConflictError` names the run holding the lock). Match them
  with `errors.Is` and `errors.As`, never with `==` or a type assertion.
  `errorlint` enforces this.
- For a missing file, use `errors.Is(err, fs.ErrNotExist)`, not
  `os.IsNotExist`. `os.IsNotExist` does not unwrap, so it breaks as soon as
  someone wraps the error.
- Only discard an error on purpose (`_ = st.Close()`), and only for best-effort
  work whose failure can't be acted on: a history append, a transcript line.
  If the reason isn't obvious from the call, add a comment saying why.
- Error text is for the person at the terminal, so say what to do next. The
  lock conflict message is the model: it names the running operation and gives
  the exact `labctl runs cancel` command to stop it.

## 4. Concurrency

- Goroutines that do cluster work belong to the run engine, which owns their
  cancellation and shutdown. A new `go func()` in a handler is a sign the work
  should be a `run.Spec`.
- Guard every map that more than one goroutine touches, or use `sync.Map` for
  write-once, read-many data (`run.Engine.specs`).
- Never mutate a value that a cache hands out. `scenario.(*Engine).Get` writes
  to a shared `*Scenario`, and the race detector catches it in
  `service/scenario`: [B13](backlog.md#b13--scenarioengineget-writes-to-a-shared-cached-scenario).
  Return a copy, or compute the field on read.
- Prefer `wg.Go(f)` over `wg.Add(1)` followed by `go func() { defer wg.Done() }`,
  and prefer the `atomic.Int64` type over the `atomic.AddInt64` functions.
- `make test` runs with `-race`. A race report is a bug, never a flaky test.

## 5. HTTP handlers

A handler is decode → validate → call → respond:

```go
func (s *Server) handleScenarioDown(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r, "scenario") // answers 400 itself
	if !ok {
		return
	}
	...
	respondJSON(w, http.StatusOK, result)
}
```

- `pathName` validates the `{name}` route variable. Don't write the regex
  check again in each handler.
- Failures go through `respondError(w, r, status, code, msg)`, which returns
  `application/problem+json` with a stable `code` ([ADR-0006](adr/0006-api-conventions.md)).
- Metrics, access logs, auth and CORS are middleware. Never add them inside a
  handler.
- Use `http.MethodPost` and `http.StatusOK`, not string or number literals.
  `usestdlibvars` enforces this.

Adding a route: follow the `api-change` skill or [ADR-0006](adr/0006-api-conventions.md).

## 6. CLI commands

New commands are built by a constructor, so tests can run a fresh tree without
shared flag state:

```go
func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that your environment can run SnowOps Labs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), toolchain.NewExec())
		},
	}
}
```

Declare a command's flag variables inside its constructor, so each tree
gets its own copies.

- Keep `RunE` down to parsing flags and calling one function. That function
  takes an `io.Writer` (`cmd.OutOrStdout()`) and a context, so a test can check
  what it prints without a terminal. `runDoctor` is the pattern to follow.
- Don't add new package-level `var fooCmd = &cobra.Command{…}` values or flag
  variables. Older commands still use them
  ([B18](backlog.md#b18--internalcli-runs-on-package-level-state)).
- Every change to the command surface also updates the [CLI reference](reference/cli/index.md).

## 7. Naming

- Don't shadow an imported package or a predeclared identifier. `internal/cli`
  once had a package-level variable named `exec`, which forced every file that
  needed `os/exec` to import it under an alias. It is now `scriptExec`. The
  `predeclared` linter rejects variables named `len`, `max`, `real`, `any` and
  the like.
- Package names are short, lowercase and singular, with no `util` or `common`.
  When two packages share a name, alias the import at the use site
  (`scnsvc "…/internal/service/scenario"`).
- Getters don't take a `Get` prefix unless the method does real work, such as a
  lookup that can fail.

## 8. Comments

- The package doc comment explains why the package exists. See
  `internal/run/engine.go`.
- Exported identifiers should have a doc comment that starts with their name.
  The linter doesn't enforce this yet, so reviewers check it.
- A doc comment sits directly above the declaration it describes. A comment
  separated from its declaration is attached to whatever follows it, and
  `go doc` shows it there.
- Say why, not what. Keep comments under three lines and leave out task and
  ticket numbers ([invariants](AGENT-CONTEXT.md#invariants)).
- Describe the code as it is now. A reader should understand it without
  knowing its history, so leave out "used to", "v1", "the old path" and
  accounts of past bugs. Put that story in the commit message.
- Delete comments that repeat the code, such as `// Get pods` above a call
  to `GetPods`.
- Every package has exactly one `// Package x ...` comment, in its main file.
  Leave a blank line between the `// SPDX-License-Identifier` header and
  `package`. Without it, Go treats the licence line as the package's
  documentation.

## 9. Linting

`src/.golangci.yml` is the only lint policy. It turns on the correctness
linters (errcheck, govet, staticcheck, errorlint, bodyclose, noctx, nilerr,
gosec) and the idiom linters (modernize, usestdlibvars, predeclared, intrange,
copyloopvar, perfsprint, gocritic).

```bash
make lint-go                            # the gate CI runs
cd src && golangci-lint run --fix ./... # applies the automatic fixes
```

To suppress a finding, name the linter and give the reason at the call site:

```go
cmd := exec.Command("sudo", args...) //nolint:gosec,noctx // re-exec of this CLI under sudo
```

A bare `//nolint`, or one without a reason, fails review.

## Before you open a PR

- [ ] `make fmt lint test` passes, including `-race`.
- [ ] New I/O takes a context, and new errors wrap with `%w`.
- [ ] No new package-level mutable state and no new `go func()` in a handler.
- [ ] The docs this change touches are updated in the same PR, and
      `make docs-check` passes.
