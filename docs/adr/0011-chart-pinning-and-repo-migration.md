# ADR 0011 — Pin every chart; migrate off the deprecated Grafana charts

**Status:** Accepted
**Date:** 2026-09-04
**Wave:** W4

## Context

Two problems surfaced while auditing the platform's Helm usage.

**Unpinned charts.** Eight components ran `helm upgrade --install` with no
`--version`: ingress-nginx, traefik, kube-prometheus-stack, grafana, loki,
tempo, argo-cd, chaos-mesh, kyverno and cert-manager. Helm resolves that to
whatever is newest in the repo index at that moment, so two `labctl init` runs a
week apart produce different clusters. A lab that "worked last week" cannot be
reproduced, and a scenario that breaks cannot be attributed to a change we made.

**Deprecated charts.** Checked against the live repos:

| Chart | `deprecated` | Note |
|---|---|---|
| `grafana/grafana` | yes | maintained in `grafana-community` from 2026-01-30 |
| `grafana/tempo` | yes | same migration |
| `grafana/promtail` | yes | **end of life, no successor in either repo** |
| `grafana/loki` | no | also moving to `grafana-community` |

Separately, `kubernetes-dashboard` is installed from a release tarball in the
`kubernetes-retired` org because both its Helm repo (404) and OCI registry (403)
are gone.

## Decision

1. Every chart is pinned, and every pin lives in `config/versions.env` with a
   link to the project's releases page. Install scripts read the pin from the
   environment with the same value as a default, so a script run outside
   `labctl` still installs a known version.
2. Grafana, Loki and Tempo move to `https://grafana-community.github.io/helm-charts`
   (`grafana-community/grafana` 13.2.0, `grafana-community/loki` 18.12.0,
   `grafana-community/tempo` 2.3.0). Each was verified by rendering the
   platform's existing values against the new chart before the switch.
3. Promtail stays on `grafana/promtail`, pinned at 6.17.1, and keeps shipping
   logs. It is deprecated and EOL, so this is a dated decision, not a
   preference — see ADR-0012 for how Alloy enters the stack ahead of the log
   migration.
4. Strimzi moves from 0.47.0 to 1.2.0, which is KRaft-only. The lab's CRs are
   already KRaft + `KafkaNodePool`, but 1.2.0 ships broker images for Kafka
   4.2.0–4.3.1 only, so `KAFKA_VERSION` moves 3.9.0 → 4.3.1 in the same change
   and the console-tool client images follow.
5. `grafana`'s image tag moves from `latest` to a pinned `13.2.1`.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep floating versions for "always current" labs | The lab's value is a reproducible cluster. An upstream chart change silently altering a lesson is a worse failure than being a version behind. |
| Pin inside each `install.sh` only | The pins could not be reviewed together or overridden per environment, and drift between them would be invisible. |
| Stay on the `grafana/` repo until it stops publishing | The deprecation warning prints on every install and teaches learners to ignore warnings. |
| Take Alloy for logs now and drop Promtail | A large change to the log pipeline while log delivery was itself being debugged. Deferred deliberately. |

## Consequences

- Upgrading a component is a reviewable one-line change with a known blast
  radius.
- `config/versions.env` becomes a required stop when adding a platform component
  — enforced by review, and stated in `CLAUDE.md`.
- **The Strimzi 1.x jump cannot be done in place, and this was proven on a real
  cluster (R13).** Three separate walls, in order:
  1. `helm upgrade` never updates a chart's CRDs — `crds/` is install-only. The
     operator came up querying `core.strimzi.io/v1` against a cluster that only
     served `v1beta2` and crash-looped. `install.sh` now applies the pinned
     chart's CRDs with server-side apply before upgrading, but only when the
     release already exists (on a first install Helm owns those fields, and
     pre-applying them causes a field-manager conflict on `.spec.versions`).
  2. Strimzi 1.x drops `v1beta2` entirely, and Kubernetes refuses to remove a
     version still listed in a CRD's `status.storedVersions` without a storage
     migration. There is no migration across this boundary, so `install.sh`
     detects that specific error and stops with instructions instead of ten
     unreadable CRD errors.
  3. `helm uninstall` does not delete CRDs either, so `platform down data/kafka`
     used to report success while leaving ten cluster-scoped CRDs behind — which
     made the documented "down then up" recovery fail too. `uninstall.sh` now
     removes them (`KAFKA_KEEP_CRDS=true` to opt out).

  The supported path is therefore `labctl platform down data/kafka` then
  `labctl platform up data/kafka`, with Kafka's ephemeral storage meaning no
  data is lost that a restart would not already have lost.
- Strimzi 1.x also changes the CRs themselves: `apiVersion` becomes
  `kafka.strimzi.io/v1`, and `resources` moves from `spec.kafka` onto the
  `KafkaNodePool` (the pool owns the pods, so it owns their sizing).
- The pins go stale. That is the intended trade: staleness is visible in one
  file, whereas drift is not visible at all.
