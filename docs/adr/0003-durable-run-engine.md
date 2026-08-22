# ADR 0003 — A durable run engine over shell scripts, not a Kubernetes operator

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W1

## Context

The v1 executor called `exec.Command(...).Run()` with no `context.Context`. That
one omission caused most of the tool's operational problems: a 20-minute
`helm install` could not be aborted, there were no timeouts, a killed server
orphaned child processes, and the UI's cancel affordance did not exist because
it could not have worked.

"Production grade" for a tool whose every operation is a long-running mutation
of cluster state means: you can stop it, you can time-bound it, you can see what
it did, and you can read that back tomorrow.

## Decision

Build a durable run engine in `internal/run`. Shell scripts remain the unit of
work; the engine wraps them with:

- a `context.Context` on every run, with a per-kind timeout,
- **process-group execution** (`Setpgid`), so cancellation reaches `helm`'s
  children — `SIGTERM` to the group, grace period, then `SIGKILL`,
- durable run records and **every output line persisted with a monotonic
  sequence number**, streamed to consumers from a cursor,
- structured step tracking parsed from script-emitted markers,
- graceful shutdown that cancels and waits for in-flight runs.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Patch the existing executor | Cancellation, durability and locking are cross-cutting; bolting them on leaves the same shape with more edge cases |
| Add a declarative desired-state reconciler | Real value, but a large design surface. We take the 80% — a `components` inventory table — and skip drift correction (see Consequences) |
| Kubernetes operator with CRDs | Most "K8s-native", but a near-total rewrite, requires a cluster to do anything at all, and makes the laptop-first golden path heavier. Rejected on those grounds |

## Consequences

- **Easier:** cancel works everywhere, uniformly. The UI run console becomes
  possible. Support questions are answerable from `labctl runs logs <id>`.
- **Harder:** scripts must be well-behaved on `SIGTERM`. We document the
  contract and test it; scripts that ignore it get `SIGKILL`ed, which is why the
  component inventory records intent before execution.
- **Not doing:** drift detection. If a user `kubectl delete`s something behind
  our back, `lab status --live` will show it but nothing auto-corrects. Recording
  what we installed is enough for exact teardown, which is the actual pain point.
- Process-group handling is platform-specific; the Unix implementation is
  guarded by build tags with tests on both macOS and Linux runners.
