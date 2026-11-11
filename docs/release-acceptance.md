# Release acceptance

Before a branch merges to `main` for a release, walk the lab as a learner on
the machine you develop on. The hermetic suites (`make test`, `make lint`)
prove the code; this proves the product: that `labctl init` brings a lab up,
its URLs and dashboards work, the UI answers, scenarios grade, and a fault can
be injected and resolved. It has two halves.

1. **Regression journey**: `scripts/release-acceptance.sh`, the same steps
   every time, so nothing that used to work silently stops working.
2. **Change walk**: the checks this branch's own changes need, derived from
   `git diff main...HEAD` and run by hand or by the
   [`release-acceptance` skill](../.claude/skills/release-acceptance/SKILL.md).

A branch is ready to release when both are green and every finding is fixed or
filed as a [GitHub issue](https://github.com/snowops-oss/snowopslabs/issues).

## Before you start

- A live lab on this machine (`kubectl config current-context` names it). The
  journey never creates, resets or tears down a lab; it converges the one you
  have with `labctl init`, as a learner does after an upgrade.
- No incident active (`labctl incident status`); the incident step skips
  otherwise.
- Room to spare if you cycle a scenario (`RA_SCENARIO`); see
  [resources](resources.md).

## The regression journey

```bash
scripts/release-acceptance.sh              # everything, gates included (~6 min)
scripts/release-acceptance.sh --quick      # skip make test / lint / docs-check
scripts/release-acceptance.sh --only init,datasources,incident
RA_SCENARIO=autoscaling-under-load scripts/release-acceptance.sh --quick
```

Each step writes `<step>.log` and one row of `report.md` in
`~/.snowops/acceptance/<UTC time>/` (`RA_OUT` moves it). Every step runs even
after one fails, and the run exits non-zero if any failed.

| Step | Passes when |
|---|---|
| `build` | `make cli-build` builds `bin/labctl`. Warns when the `labctl` on `PATH` is another build, since learners run that one. |
| `gates` | `make test`, `make lint` and `make docs-check` pass. |
| `content` | `labctl validate` finds every scenario, incident, challenge and path valid. |
| `doctor` | `labctl doctor` says the machine is ready. |
| `init` | `labctl init` ends with "Lab is up", which now includes its own ingress and Grafana → Prometheus checks. |
| `status` | `labctl status` reports nothing unreachable or not running. |
| `urls` | Grafana's `/api/health` and Prometheus's `/-/ready` answer on the lab's own URLs. |
| `datasources` | Every Grafana datasource passes its health check. |
| `dashboards` | Grafana holds dashboards, Prometheus has targets up, and the `namespace` label that fills the Namespace picker has values. |
| `ui` | A throwaway `labctl ui` serves the SPA and `/api/v2` answers JSON for scenarios, incidents, challenges, runs, apps and status. |
| `apps` | Every deployed app has all replicas ready. |
| `scenarios` | `labctl scenario verify` grades every active scenario (a pending objective is fine; a crash is not). With `RA_SCENARIO`, that scenario is cycled up → verify → down. |
| `incident` | `RA_INCIDENT` (default `crashloop-bad-config`) is injected, its detection check fires, `labctl incident resolve` clears it, and every app is ready again. The resolve always runs. |
| `learning` | `labctl learn list`, `challenge list` and `challenge info` answer. |
| `leaks` | No namespace exists that did not exist before the run. |

A failing step is a finding, not a flaky test: open its log, reproduce it with
`--only <step>`, and fix the cause.

## The change walk

The journey cannot know what this branch changed. For every user-visible
change in `git diff --stat main...HEAD`, read the commit, then check it the way
a learner meets it:

| The branch touched | Check by hand |
|---|---|
| `src/internal/cli/` (a command) | Run the command, its `--help`, and each error path its docs name. |
| `src/internal/httpapi/`, `src/ui/` | Open `labctl ui` in a browser; use the changed page; the console has no errors. |
| `platform/<category>/<component>/` | `labctl platform up <target>` on the live lab converges; the component's URL or status works. |
| `runtimes/` | The runtime's lifecycle: `labctl init` on an existing lab, and after `colima stop` (or a node container restart). |
| `scenarios/<name>/` | The [scenario review](authoring/scenario-review.md) P2 walk: up, the objectives, verify, down. |
| `incidents/<name>/` | Inject, confirm detection, resolve; a false fix does not pass. |
| `docs/` | The commands the changed page shows run as written. |

Record each check with its result in the report's "Change walk" section.
