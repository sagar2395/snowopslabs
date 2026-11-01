# Incidents

`labctl incident` injects realistic, reversible production faults from
`incidents/` so you can practise diagnosing them. Each fault ships a detection
check that confirms when you have actually fixed it. The fault contract is
[incidents/README.md](../../../incidents/README.md).

```bash
labctl incident list                          # browse the fault library
labctl incident info oom-kill                 # details, upstream references, applyable snippets
labctl incident inject oom-kill               # break the lab on purpose
labctl incident status                        # run the detection check
labctl incident hint                          # next progressive hint (recorded)
labctl incident solution                      # full walkthrough (spoiler; asks to confirm)
labctl incident history                       # past runs: time-to-check, MTTR, hints, resolved-by
labctl incident resolve                       # escape hatch: undo the active fault
labctl incident resolve oom-kill              # works even if active state was lost
```

## Room to inject

`labctl incident inject` runs the same capacity check as `scenario up`: a fault
whose `fault.yaml` declares `requirements.memory` (a noisy neighbour, say) is
blocked when Docker lacks room for it, with the same advice. Injecting into an
app an active scenario is graded against prints a warning.

## Which application gets broken

A fault names its workload through `{{.WorkloadName}}` rather than an app
([ADR-0014](../../adr/0014-workload-binding-and-app-contract.md)), so any fault
can be injected into any app in the lab:

```bash
labctl incident inject oom-kill --app java-api
```

The app is **recorded with the incident**. `status`, `hint` and `resolve` read it
back, so they act on the workload that was broken rather than on whatever
`APP_NAME` says - a detection check pointed at the wrong namespace passes, and a
passing check clears the incident and scores it solved.

The UI asks the same question in its inject dialog.

## Game-day mode

```bash
labctl incident inject --random --silent      # surprise fault, name hidden
labctl incident inject --random --seed 42     # reproducible pick for a whole team
labctl incident inject --random --category network
```

`inject` flags:

| Flag | Default | Meaning |
|---|---|---|
| `--random` | off | pick a random eligible fault |
| `--seed` | - | seed for `--random`, for reproducible team exercises |
| `--category` | - | restrict `--random` to `workload`, `network`, `resources`, `storage` or `config` |
| `--silent` | off | do not reveal which fault was injected: the inject prints neither the fault's name nor its script's output, `incident list` does not mark the active fault, unresolved `incident status` says only `NOT RESOLVED - keep digging, or: labctl incident hint` (a check error may say it could not run, without the check name or output), resolved status names the check and fault, the inject arms its alert rule under the neutral name `labfault-silent`, so Prometheus' Alerts page does not name the fault, and the run history records the inject with the target `(hidden)` and withholds its output (`labctl runs logs` and `GET /runs/{id}/logs` refuse it) |
| `--force` | off | inject even when another incident is active |
| `--deploy-prereqs` | off | install the platform components the fault needs, and build and deploy the apps it lists under `prerequisites.apps` (and its target, when that is an app), if they are not running. Without it, a missing app stops the inject with the command that deploys it. |

## Rules and timing

- One active incident at a time; `--force` overrides.
- Injection is gated on the fault's prerequisite apps being present.
- `inject` first checks that the cluster answers. When it does not, it says so
  and names `labctl status` and `labctl init`; it never reports the app as "not
  deployed" because the cluster could not be asked.
- `resolve.sh` always restores the lab, regardless of partial manual fixes.
- The first `incident status` call timestamps *time-to-check*, the detection
  proxy. Resolution (the detection check passing, or the escape hatch) closes
  the run.
- When a hand fix makes `incident status` pass, it then runs the fault's
  `resolve.sh` as a recorded resolve. That removes the armed alert rule and the
  `labfault-*` bookkeeping a fix leaves behind; the run still counts as solved.
- Each run is appended to `~/.snowops/state/<cluster>/history/results.jsonl` with MTTR, hints
  used, and whether it was resolved manually or via `resolve`. The escape hatch
  counts as a non-completion for challenge scoring.

## On-call drills

Every shipped fault declares an `expectAlert` and arms its PrometheusRule when
injected (the alert names are in each `fault.yaml`). Alertmanager routes
the firing alert to the in-cluster pager (`labctl service up pager`), or to
`ALERT_WEBHOOK_URL` if that was set when `monitoring/metrics` was installed.

`incident status` reports whether the page fired by querying Alertmanager at
`ALERTMANAGER_URL`, defaulting to `http://alertmanager.<DOMAIN_SUFFIX>` through
the ingress the metrics provider creates.

REST: `GET /api/v2/incidents`,
`POST /api/v2/incidents/{name}/inject[?app&silent&force]`,
`POST /api/v2/incidents/inject-random[?app&seed&category]`,
`GET /api/v2/incidents/status`, `POST /api/v2/incidents/hint`,
`GET /api/v2/incidents/history`, `POST /api/v2/incidents/resolve[?name=]`.
Silent mode hides the fault's identity in API responses until it is resolved:
`GET /incidents` and `GET /incidents/status` answer `"fault": "(hidden)"` for
the active incident. While unresolved, the status check contains only
`"pass": false`, with no check name or output. Once resolved, the normal check
result is returned. A silent inject from the UI broadcasts no job label or
script output, and `GET /runs/{id}/logs` for its run answers `403 hidden_run`.
`inject-random` picks among the faults eligible for the app named in `app`, and
answers `409 no_eligible_fault` when there are none. `hint` and `resolve` answer
`409 no_active_incident` when nothing is injected.
