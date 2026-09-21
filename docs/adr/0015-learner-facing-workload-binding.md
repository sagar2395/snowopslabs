# ADR 0015 — The workload binding, where the learner touches it

**Status:** Accepted
**Date:** 2026-09-11

## Context

[ADR-0014](0014-workload-binding-and-app-contract.md) made scenarios run against
any app, and the engine binds everything it reads or runs itself. Running a
scenario against echo-server showed where the binding still stopped: at the
learner's own terminal and browser.

- Scripts a learner runs by hand read `WORKLOAD_NAME`, which only the engine
  exports. From a terminal they fell back to `go-api`, so
  `build-image.sh` built go-api and `enable-tracing.sh` patched go-api while the
  scenario was graded against echo-server.
- `labctl traffic start` always loaded go-api.
- Dashboards baked the app into every query, so a dashboard could only ever show
  the app it was installed for, and a scenario could never be active for two
  apps without its dashboards colliding.
- `/etc/hosts` came from a hostname list in the binary. It knew
  `go-api-dev.k3d.local` but not `echo-server-dev.k3d.local`, and never
  `vault.k3d.local`.

One later requirement shapes all of these: a scenario may one day be active for
two apps at once. Any answer that looks up "the app this scenario is active for"
becomes ambiguous then.

## Decision

1. **Hand-run scripts take the binding as flags.** Scenario scripts source
   `scenarios/_lib/workload.sh`, which reads `--app` and `--namespace`, falls
   back to the engine's environment, and otherwise stops with a usage error.
   Commands in content are written with `--app {{.WorkloadName}} --namespace
   {{.WorkloadNamespace}}`, so the text a learner copies is already filled in.
   `labctl validate` fails a command that omits `--app`. `labctl traffic start`
   follows the same rule with `--app`.
2. **Dashboards select the app.** A scenario dashboard declares `app` and
   `namespace` variables that default to the activation's values, and its queries
   use `$app` and `$namespace`. There is one shared Application Request Metrics
   dashboard with a multi-select App variable, not a copy per app.
3. **The Ingress is the source of truth for hostnames.** `labctl hosts add`
   writes the platform hostnames, one per app under `apps/`, and every Ingress
   host in the cluster under the domain suffix.
4. **An exercise is a field, not a sentence.** `exercise: true` on a snippet
   replaces "APPLY THIS ONE YOURSELF" in its label, and the UI offers a
   ready-to-run apply command.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| `labctl scenario env <name>`, which scripts call to look up the active app | Ambiguous once one scenario is active for two apps; adds a command whose only job is a lookup; hides from the learner which app a command acts on |
| Environment variables in front of each command (`WORKLOAD_NAME=… bash …`) | Long, exposes internal names, and dropping one variable silently reintroduces the go-api fallback |
| `labctl scenario exec <name> -- script.sh` | Hides the script the learner is meant to read, and adds interactive streaming to the run engine for no grading benefit |
| One provisioned dashboard per app | Cannot compare apps side by side, and needs its own create and delete lifecycle whenever an app is added or removed |
| Wildcard DNS for the domain suffix | `/etc/hosts` has no wildcards; a resolver needs a local DNS daemon or a different domain, which is a larger change for every existing lab |

## Consequences

- A command copied from a README without flags fails with a usage error naming
  the fix, instead of acting on the wrong app.
- The App selector lists apps that expose the contract's request metric, plus
  the activation's own app whatever it exposes. The shared dashboard queries the
  semconv name directly, so an app declaring a different `APP_REQUEST_METRIC`
  does not appear there.
- `hosts add` still needs a re-run when a scenario adds a hostname; `scenario up`
  and `app deploy` now say when that is.
- Parallel activation remains unbuilt. These choices do not have to be revisited
  for it.
