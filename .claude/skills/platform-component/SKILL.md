---
name: platform-component
description: "Add, upgrade, debug or remove a SnowOps Labs platform component — Helm charts, chart pinning, values ownership, CRDs, StatefulSet immutability and provider selection. Use whenever a task involves anything under platform/, config/versions.env, a helm install or upgrade, or a component that will not install, upgrade or uninstall cleanly."
user-invocable: true
---

# Platform components

A component lives at `platform/<category>/<component>/` and is installed by
shell and helm, orchestrated by Go. Go never grows helm logic.

## Read first

1. **[docs/runbooks/R05-platform-components.md](../../../docs/runbooks/R05-platform-components.md)**
   — how to add one, and every Helm trap this repo has hit.
2. [ADR-0010](../../../docs/adr/0010-platform-values-single-source.md) — values
   ownership. [ADR-0011](../../../docs/adr/0011-chart-pinning-and-repo-migration.md)
   — chart pinning.
3. [docs/reference/cli/platform.md](../../../docs/reference/cli/platform.md) —
   the commands and how targets and providers resolve.

Observability components additionally:
[R13](../../../docs/runbooks/R13-observability-pipeline.md).

## Adding one — all in the same change

1. The component directory with **exactly one** `values.yaml`.
2. A chart version pin in `config/versions.env`. There is no
   `helm upgrade --install` without `--version`.
3. Install and uninstall through `helm_upgrade_install` from
   `platform/_lib/helm.sh`, never a raw `helm` call.
4. A `status.sh` so `platform status --live` can report on it.
5. Documentation: R05, and the component in the reference.

## The traps

**`helm upgrade` never updates CRDs.** `crds/` is install-only, so an operator
chart's major bump leaves stale CRDs and a crash-looping operator. Apply the
pinned chart's CRDs with `kubectl apply --server-side` first — but only when the
release already exists, or Helm's own CRD install conflicts on `.spec.versions`.

**`helm uninstall` never deletes CRDs either.** A component whose uninstall
leaves cluster-scoped CRDs behind is not uninstalled, and the next install
inherits a CRD whose `status.storedVersions` can block it outright.

**Most of a StatefulSet spec is immutable.** `helm_upgrade_install` recovers by
deleting the controller with `--cascade=orphan` — pods and PVCs survive — and
retrying. Use it for anything StatefulSet-backed.

**Values live in one place.** A scenario that needs your component points at
your `values.yaml` with `platformValues:` and overlays only its differences. A
second full copy drifts, and Helm turns the drift into an immutable-field error.

**A scenario that adopts your release must not uninstall it.**

## Observability components

If the component emits metrics, logs or traces, read
[R13](../../../docs/runbooks/R13-observability-pipeline.md) before wiring it up
— every failure mode there is silent. In particular: label every
ServiceMonitor, PodMonitor and PrometheusRule `release: prometheus`, and set the
three `*SelectorNilUsesHelmValues: false` flags, because an empty selector does
*not* mean "select everything".

## Verify your work

```bash
labctl platform up <category>/<component>
labctl platform status <category>/<component> --live
labctl platform up <category>/<component>      # again — must be a clean no-op
labctl platform down <category>/<component>
labctl platform up <category>/<component>      # and reinstall cleanly
```

The second install is the one that catches immutable-field and stale-CRD
failures. Do not skip it.

## Before you finish

- [ ] One `values.yaml`, no duplicates anywhere.
- [ ] Chart version pinned in `config/versions.env`.
- [ ] Uses `helm_upgrade_install`, not raw `helm`.
- [ ] Install → reinstall → uninstall → reinstall is clean.
- [ ] Uninstall leaves no cluster-scoped CRDs behind.
- [ ] Scripts are POSIX and pass `make lint-shell`.
- [ ] R05 updated; an ADR added if the decision is notable.
- [ ] `make docs-check` passes.
