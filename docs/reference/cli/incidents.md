# Incidents

`labctl incident` injects realistic, reversible production faults from
`incidents/` so you can practise diagnosing them. Each fault ships a detection
check that confirms when you have actually fixed it. The fault contract is
[incidents/README.md](../../../incidents/README.md).

```bash
labctl incident list                          # browse the fault library
labctl incident list -o json                  # the same as a JSON array, for scripts
labctl incident info oom-kill                 # details, upstream references, applyable snippets
labctl incident inject oom-kill               # break the lab on purpose
labctl incident status                        # run the detection check
labctl incident hint                          # next progressive hint (recorded)
labctl incident solution                      # full walkthrough (spoiler; asks to confirm)
labctl incident history                       # past runs: time-to-check, MTTR, hints, resolved-by
labctl incident resolve                       # escape hatch: undo the active fault
labctl incident resolve oom-kill              # works even if active state was lost
```

`incident list -o json` (`--output json`) prints only a JSON array on stdout:
each fault as `GET /api/v2/incidents` returns it (`name`, `displayName`,
`description`, `category`, `severity`, `target`, `prerequisites`,
`requirements`, `detection` and the rest of `fault.yaml`), plus `active`. A
silently injected fault is never marked active. Load warnings go to stderr.

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
  used, and whether it was resolved manually or via `resolve`
  ([recorded results](#recorded-results)). The escape hatch
  counts as a non-completion for challenge scoring.

## Recorded results

Every finished incident, challenge, scenario verification, learning module and
comparison is a line in `~/.snowops/state/<cluster>/history/results.jsonl`.
The file's own format is internal; `labctl results export` is the supported
way to read it.

```bash
labctl results export > results.json                       # every run, as JSON
labctl results export --kind incident --since 2026-11-01 -o csv > game-day.csv
labctl results export --since 4h -o csv                    # runs that started in the last 4 hours
labctl results card                                        # Markdown card for your latest run
labctl results card oom-kill --by "Team Blue" -o svg > card.svg
```

### `labctl results export`

| Flag | Default | Meaning |
|---|---|---|
| `-o`, `--output` | `json` | `json` (an array) or `csv` (a header row, then one row per run) |
| `--kind` | all | `incident`, `challenge`, `scenario`, `module` or `comparison` |
| `--since` | - | runs that started at or after this: a date (`2026-11-01`, local midnight), an RFC 3339 time, or a duration back from now (`4h`) |
| `--until` | - | runs that started before this, in the same forms |

Runs come out oldest first. Each has these fields, in this order in the CSV.
Fields may be added in a later release; none is renamed or removed.

| Field | Meaning |
|---|---|
| `kind` | `incident`, `challenge`, `scenario`, `module` or `comparison` |
| `name` | the fault, challenge or scenario; `<path>/<module>` for a module |
| `user` | who ran it: the logged-in user under `LABCTL_AUTH`, otherwise the OS user (`unknown` when neither is known) |
| `workload` | the app it ran against, when recorded |
| `startedAt`, `endedAt` | RFC 3339, UTC |
| `elapsedSeconds` | wall-clock seconds; for an incident, time to resolve |
| `hintsUsed` | hints revealed |
| `score` | 0-100, or `null` in JSON and empty in CSV for a run that is not scored, such as an incident outside a challenge |
| `outcome` | as recorded: `resolved` or `auto-resolved` for an incident, `passed`, `failed` or `aborted` for a challenge, `passed` or `failed` for a scenario, `completed` for a module, `measured` for a comparison |
| `resolvedBy` | for an incident, `fix` (the detection check passed after your fix) or `escape-hatch` (`labctl incident resolve` put the lab back); empty for other kinds |

A history line that is not a valid record is skipped, and stderr names the
file and the line numbers, so the export on stdout is still parseable. A CSV
text value that starts with `=`, `+`, `-` or `@` gets a leading `'`, so a
spreadsheet does not run it as a formula.

### `labctl results card`

Prints a card for the most recent incident, challenge or scenario run, or the
most recent run of the name given, as Markdown (`-o markdown`, the default) or
an SVG image (`-o svg`). It is made on your machine and nothing is uploaded.

The card shows the run, the time to resolve and hints used (for an incident),
the score (for a challenge) or checks passed (for a scenario), how it ended
and the date. An incident ended with `labctl incident resolve` says
`escape hatch` instead of claiming a fix. Every card, in both formats, ends
with:

> Self-reported: recorded on a personal lab and not verified by the SnowOps
> Labs project.

The history is a local file anyone can edit, so the card never suggests the
project checked it. It carries no user, app, cluster, host or path; `--by`
adds a name of your choosing. `--kind` limits it to `incident`, `challenge` or
`scenario`.

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
answers `409 no_eligible_fault` when there are none. A second inject while one
is active answers `409 incident_active`; for a silent incident the message does
not name it, and neither does `labctl incident inject`. With auth on, inject,
`inject-random` and `resolve` are operator-only: a participant gets
`403 forbidden_role`. Status and hints are open to participants. `hint` and `resolve` answer
`409 no_active_incident` when nothing is injected.
