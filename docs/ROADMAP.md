# SnowOps Labs — Roadmap

> Product rationale: [`PRODUCT.md`](PRODUCT.md). How it fits together:
> [`architecture/ARCHITECTURE.md`](architecture/ARCHITECTURE.md).

This is a living, high-level view of where SnowOps Labs is and where it's going.
It's an early release: the core loop works, and the surrounding surfaces are
being built out in the open. Priorities shift with feedback — if something here
matters to you, [open an issue](https://github.com/sagar2395/snowopslabs/issues)
or 👍 an existing one.

## Works today

- **Cluster lifecycle** — stand up a production-shaped cluster locally (`k3d` /
  `kind`) with a real platform stack (ingress, Prometheus, Grafana, Loki, …),
  and tear it down cleanly. Idempotent: interrupt and re-run and it converges.
- **Scenarios** — activate a realistic situation, explore it, and verify your
  work against machine-checkable objectives that report *why* they failed.
- **Incidents** — inject a reversible fault, detect its resolution, and get
  scored on how you fixed it.
- **Content model** — every scenario, incident, learning path and challenge is
  schema-validated and cross-referenced (`labctl validate`); author your own and
  drop them in via `SNOWOPS_CONTENT_PATH` without forking.
- **A durable core** — every operation that shells out is cancellable,
  time-bounded, and recorded, so history survives restarts.

## In progress / next

- **Lab & platform lifecycle on the durable engine** — moving the remaining
  cluster/component operations onto the cancellable, recorded run engine, with
  exact teardown and instant status.
- **The simulation loop, hardened** — a curated, verified set of scenarios and
  incidents with reproducible scoring (MTTD/MTTR), progressive hints, and a
  traffic generator.
- **A versioned, hardened HTTP API** and a rebuilt web UI — a run console you
  can watch, deep-linkable views, and honest loading/empty/error states.
- **Learning & assessment in the UI** — guided paths, timed challenges, and a
  results view with score breakdowns.
- **Team mode & packaging** — an in-cluster server for shared game days, a Helm
  chart, and signed release artifacts.

## How releases work

Changes land in small, independently mergeable increments that keep `main`
working. Every feature ships with tests at each applicable layer (see
[`TESTING.md`](TESTING.md)) and a runbook a human follows before it merges (see
[`runbooks/`](runbooks/)). Versioning is [SemVer](../RELEASING.md); pre-1.0,
minor releases may include breaking changes, each called out in the notes.

## Get involved

Bugs, scenario/incident ideas, and PRs are all welcome — start with
[`CONTRIBUTING.md`](../CONTRIBUTING.md).
