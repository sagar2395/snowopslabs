# Extension Seams (resolvers, hooks)

SnowOps Labs keeps a small, documented seam so custom or private builds can plug
in behaviour without forking the engine. Everything in the open repository
behaves identically whether or not anything is injected — no business logic ever
enters the open engine.

This package is part of the public SDK and is CODEOWNERS-locked:

- [`pkg/extension`](../../pkg/extension/) — where content comes from
  (resolvers) and what happens around lifecycle phases (hooks).

> The entitlement seam and the OCI/pack resolver that used to live here were
> removed in v2 — see
> [ADR-0001](../adr/0001-cut-cloud-and-commercial-scope.md) and
> [ADR-0008](../adr/0008-content-extensibility-seam.md). Content distribution is
> now plain directories plus `SNOWOPS_CONTENT_PATH`.

## Resolvers — where content comes from

```go
type Resolver interface {
    CanResolve(ref string) bool
    Resolve(ctx context.Context, ref, destDir string) error
}
```

A `Chain` tries each resolver in order and uses the first that reports
`CanResolve`. The built-in open resolvers are:

| Resolver | Handles |
|---|---|
| `GitResolver` | `https://…`, `git+https://…@ref`, ssh URLs — clones a shallow content snapshot and drops `.git` |
| `LocalResolver` | `file://…` and existing local directories — copies the tree |

`DefaultResolver()` returns `Chain{GitResolver{}, LocalResolver{}}`.

To add a source, implement the interface and put it ahead of the chain:

```go
chain := append(extension.Chain{myResolver{}}, extension.DefaultResolver()...)
```

`GitResolver` takes a `GitRunner` so tests can inject a stub instead of shelling
out to real `git`.

## Hooks — around lifecycle phases

```go
type Hooks interface {
    PreStage(ctx context.Context, ev Event) error
    PostStage(ctx context.Context, ev Event) error
}
```

`DefaultHooks()` is a no-op. A `Pre*` hook returning an error aborts the phase,
so a custom build can enforce policy without the engine knowing the policy
exists. Hooks receive an `Event` naming the scenario and stage.

## Rules for anything built on these seams

- The open engine must behave identically with the default implementations.
- Custom logic lives in a **separate** repository and is injected at
  construction — never merged into the open engine.
- Changes to these interfaces follow the
  [SDK & Schema Stability Policy](sdk-stability-policy.md).
