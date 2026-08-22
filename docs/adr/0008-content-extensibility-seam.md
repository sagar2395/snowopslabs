# ADR 0008 — Content paths and an extension seam instead of a marketplace

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W2

## Context

ADR-0001 removes the pack marketplace: a pack format, OCI distribution with
cosign signing, a registry index, hosted catalog client, entitlement tiers and a
marketplace UI. That was roughly 2,000 lines of Go plus workflows and docs,
serving zero users.

But the underlying need is real and permanent: **teams must be able to add their
own scenarios and platform providers without forking SnowOps Labs.** A simulator
whose content only its maintainers can extend is not much of a simulator.

## Decision

Meet the need with two small mechanisms instead of a distribution system.

1. **Content is discovered from directories.** A scenario is a directory with a
   `scenario.yaml` matching the published schema. Same for incidents, learning
   paths, challenges, and `platform/<category>/<provider>/` with its four
   scripts. Drop it in and it appears. `labctl scenario new` scaffolds it.
2. **`SNOWOPS_CONTENT_PATH`** is a list of additional content roots. A team
   keeps private scenarios in their own git repo, clones it anywhere, points the
   variable at it, and their content sits alongside the built-in catalog —
   attributed to its source in the CLI and UI.

`pkg/extension` remains as a resolver-chain seam for out-of-tree behaviour that
content cannot express, governed by `docs/authoring/sdk-stability-policy.md`.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep the full pack/OCI/registry system | Solves distribution, which git already solves, at the cost of 2,000 lines and a signing pipeline nobody uses yet |
| Keep the pack format, drop distribution | The format's value was interchange with a registry; without it, a directory is simpler and needs no tooling |
| Plugin binaries (hashicorp/go-plugin) | Heavy for content that is fundamentally YAML and shell; complicates the single-binary story |

## Consequences

- **Easier:** publishing content is `git push`. Consuming it is `git clone` plus
  an environment variable. Nothing to sign, host, index or authenticate.
- **Harder:** no discovery — you cannot browse community content from inside the
  product. Accepted for now; a curated list in the docs covers it until there is
  enough community content for the problem to be real.
- **Trust:** external content runs shell scripts with the user's cluster
  credentials. The docs must say so plainly, and content from an external root is
  badged distinctly in the UI so its origin is never ambiguous.
- If a marketplace is ever justified by actual demand, the schema-validated
  directory format is a perfectly good payload for one.
