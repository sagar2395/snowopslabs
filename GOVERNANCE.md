# SnowOps Labs Governance

SnowOps Labs is an open-source project with a **maintainer-led** governance model.
This document defines who decides what, so contributors know how decisions are
made and the project keeps a coherent architectural direction.

## Roles

### Lead maintainer (project lead)

**@sagar2395** is the lead maintainer and holds final authority over:

- Architecture and the public SDK (`pkg/`) and scenario schema
- The roadmap and release schedule, and the act of cutting/signing releases
- Licensing, trademark, and governance changes
- Adding or removing maintainers

This authority is intentional and is enforced mechanically via `CODEOWNERS` +
branch protection (the lead maintainer is a required reviewer on engine, CLI,
SDK, CI, and licensing paths).

### Maintainers

Trusted contributors invited by the lead maintainer with merge rights in specific
areas (e.g. a scenario domain, the platform modules, docs). Listed in
[MAINTAINERS.md](MAINTAINERS.md). Maintainers review and merge within their area
but do not override architectural decisions reserved to the lead maintainer.

### Reviewers / domain code owners

Contributors trusted to review and approve PRs in a specific directory via
`CODEOWNERS`, without merge authority over the engine/SDK.

### Contributors

Anyone who opens an issue or PR. Contributions require a signed
[DCO](DCO.md) and follow [CONTRIBUTING.md](CONTRIBUTING.md).

## Decision-making

- **Routine changes** (content, docs, bug fixes) merge on code-owner approval +
  green CI.
- **Architectural changes** (engine, public SDK, scenario schema, CI policy,
  licensing) require an **RFC** under `docs/rfcs/` approved by the lead
  maintainer before implementation.
- **Disagreements** are resolved by discussion; the lead maintainer is the
  tie-breaker. Reasoning is documented in the relevant issue/RFC for transparency.

## Becoming a maintainer

Sustained, high-quality contributions in an area, plus good judgement in reviews,
may lead to an invitation from the lead maintainer. There is no fixed quota; the
bar is trust and consistency.

## Changes to governance

This document and the licensing/trademark policy may only be changed by the lead
maintainer, via a PR that the community may comment on.

## Relationship to commercial offerings

SnowOps Labs's core (engine, CLI, SDK, community content) is and will remain fully
open source under [Apache-2.0](LICENSE). Premium/enterprise content and hosted
services live **outside** this repository and never gate the open core. See
[docs/strategy/OSS-COMMERCIAL-STRATEGY.md](docs/strategy/OSS-COMMERCIAL-STRATEGY.md).
