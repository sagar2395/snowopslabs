# ADR 0001 — Cut cloud runtimes and commercial surfaces

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W0

## Context

v1 shipped AKS, EKS and GKE runtimes with Terraform modules, plus a full
commercial layer — pack marketplace with OCI distribution, entitlement tiers,
CE/Pro/Enterprise editions, and a certificate issuance framework — roughly 2,000
lines of Go plus CI workflows, registry tooling and docs.

None of the cloud runtimes were ever executed against a real cloud account.
Tasks 038 and 039 ("verify AKS end to end", "verify EKS end to end") sat open
from the first milestone to the last. Meanwhile the commercial layer served zero
users, since the project has no users at all yet.

The combined effect was breadth at the expense of depth: the core loop that
every user hits first — start a cluster, run a scenario, break it, fix it — had
no cancellation, no durable state, and no shell or UI tests.

## Decision

Delete both surfaces in v2.

- **Cloud:** remove `runtimes/{aks,eks,gke}`, `foundation/terraform`, ACR/ECR
  build strategies, `docs/cloud-runtimes.md` and the cloud CI workflows. Local
  (`k3d`, `kind`) and `incluster` remain.
- **Commercial:** remove `pkg/pack`, `pkg/entitlement`, `pkg/edition`,
  `pkg/credential`, the marketplace API and UI, `registry/`, `packs/` and
  `sdk/pack-template`. Keep `pkg/extension` as a small documented seam.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep and harden everything | ~35% of remaining effort spent on features with no users, while the core stays weak |
| Keep packs, cut monetisation | Pack distribution solves a problem git and a content path already solve (see ADR-0008) |
| Keep cloud as unverified/experimental | An untested code path in the repo is a liability that still needs maintenance, review and CI time |

## Consequences

- **Easier:** the golden path gets all the attention; CI is fast because kind is
  fast; there is no cloud cost to test anything.
- **Harder:** users wanting a managed-cluster demo must bring their own kubeconfig.
  We accept this — the product's value is the simulation, not the provisioning.
- **Reversible:** the runtime profile contract is unchanged, so a cloud profile
  can be reintroduced later by anyone with an account to verify it against.
- The removed v1 code and plan are preserved by the maintainers, outside the public tree.
