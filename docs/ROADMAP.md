# SnowOps Labs roadmap

> Why the product exists: [`PRODUCT.md`](PRODUCT.md). How it fits together:
> [`architecture/ARCHITECTURE.md`](architecture/ARCHITECTURE.md).

Where SnowOps Labs is and where it's going. Releases ship on the `stable`
branch, and the rest is built in the open. Priorities
shift with feedback, so if something here matters to you,
[open an issue](https://github.com/sagar2395/snowopslabs/issues) or 👍 an
existing one. Smaller known problems are tracked as
[GitHub issues](https://github.com/sagar2395/snowopslabs/issues).

## Works today

- **The lab.** One command (`labctl init`) builds a local k3d or kind cluster
  with a real platform stack, on macOS, Linux and WSL2. It checks Docker has
  room before a scenario or fault starts, and keeps state outside your clone.
- **Scenarios.** Thirteen scenarios across observability, autoscaling, delivery,
  security, networking, data, cost and day-2 operations, each graded by checks
  against the live cluster.
- **Incidents.** Reversible faults with hints, silent and random game-day mode,
  time-to-resolve measurement, and on-call drills that page through Alertmanager.
- **Learning paths and challenges.** Guided modules gated by checks, and timed
  challenges scored on par time and hints used.
- **Any app.** Scenarios and faults bind to a workload, so they run against
  `go-api`, `java-api`, `echo-server` or an app you bring
  ([ADR-0014](adr/0014-workload-binding-and-app-contract.md)), and
  `labctl compare` runs one scenario against two apps.
- **CLI and web UI.** Every operation is cancellable and recorded, and the
  dashboard is built into the binary. Team mode adds users and a leaderboard on
  a shared in-cluster server.
- **Your own content.** Everything is schema-validated (`labctl validate`), and
  your own scenarios load from `SNOWOPS_CONTENT_PATH` without forking.

## Next

All of this is **Planned**. Each milestone lists its open issues, and most are
open to contributors. To ask for something new, open an issue; maintainers tag
requests `community-request`, and 👍 votes on them show what people want most.

- **[First run](https://github.com/sagar2395/snowopslabs/milestone/1).** Install
  to a first resolved fault in under 30 minutes on macOS, Linux, WSL2 and
  Codespaces. This covers one guided path, fixes for small machines and
  restarts, UI actions on the same durable, cancellable runs as the CLI, and a
  way to tell whether people finish: a feedback link, and a proposal for usage
  counts that stay off unless you turn them on.
- **[Platform catalog](https://github.com/sagar2395/snowopslabs/milestone/2).**
  New scenarios for what platform engineers build and run: zero-downtime
  deploys, Postgres failover, multi-tenant guardrails, a golden path for new
  services, progressive delivery, disaster recovery with volume data and a move
  to Gateway API, along with new faults.
- **[Team game day](https://github.com/sagar2395/snowopslabs/milestone/3).** An
  easier way to install the shared server, a guide to running a game day,
  sessions that survive a restart, results you can export, and a shareable
  result card marked as self-reported.

## Later

- **Hosted labs.** The same scenarios on a cluster we run, opened in a browser.
  This is a long-term goal that comes after the open-source lab has a solid
  base of users, and its shape will follow what they need. The open-source lab
  stays complete and free either way
  ([ADR-0016](adr/0016-hosted-labs-long-term.md)).

## How releases work

Changes land in small, independently mergeable pieces that keep `main`
working. Every feature ships with tests at each layer it touches (see
[`TESTING.md`](TESTING.md)) and a runbook a human follows before it merges (see
[`runbooks/`](runbooks/)). Versioning is [SemVer](../RELEASING.md), and
breaking changes follow the [stability policy](authoring/sdk-stability-policy.md).

## Get involved

Bugs, scenario and incident ideas, and PRs are all welcome. Start with
[`CONTRIBUTING.md`](../CONTRIBUTING.md).
