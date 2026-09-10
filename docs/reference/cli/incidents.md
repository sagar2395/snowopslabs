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

## Which application gets broken

A fault names its workload through `{{.WorkloadName}}` rather than an app
([ADR-0014](../../adr/0014-workload-binding-and-app-contract.md)), so any fault
can be injected into any app in the lab:

```bash
labctl incident inject oom-kill --app java-api
```

The app is **recorded with the incident**. `status`, `hint` and `resolve` read it
back, so they act on the workload that was broken rather than on whatever
`APP_NAME` says — a detection check pointed at the wrong namespace passes, and a
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
| `--seed` | — | seed for `--random`, for reproducible team exercises |
| `--category` | — | restrict `--random` to `workload`, `network`, `resources`, `storage` or `config` |
| `--silent` | off | do not reveal which fault was injected |
| `--force` | off | inject even when another incident is active |
| `--deploy-prereqs` | off | build and deploy the target app if it is not running |

## Rules and timing

- One active incident at a time; `--force` overrides.
- Injection is gated on the fault's prerequisite apps being present.
- `resolve.sh` always restores the lab, regardless of partial manual fixes.
- The first `incident status` call timestamps *time-to-check*, the detection
  proxy. Resolution — the detection check passing, or the escape hatch — closes
  the run.
- Each run is appended to `.labctl/history/incidents.jsonl` with MTTR, hints
  used, and whether it was resolved manually or via `resolve`. The escape hatch
  counts as a non-completion for challenge scoring.

## On-call drills

Faults that declare an `expectAlert` — `oom-kill`, `crashloop-bad-config` and
`bad-deploy-rollout` — arm a PrometheusRule when injected. Alertmanager routes
the firing alert to the in-cluster pager (`labctl service up pager`), or to
`ALERT_WEBHOOK_URL` if that was set when `monitoring/metrics` was installed.

`incident status` reports whether the page fired by querying Alertmanager at
`ALERTMANAGER_URL`, defaulting to `http://alertmanager.<DOMAIN_SUFFIX>` through
the ingress the metrics provider creates.

REST: `GET /api/v2/incidents`,
`POST /api/v2/incidents/{name}/inject[?silent&force]`,
`POST /api/v2/incidents/inject-random[?seed&category]`,
`GET /api/v2/incidents/status`, `POST /api/v2/incidents/hint`,
`GET /api/v2/incidents/history`, `POST /api/v2/incidents/resolve[?name=]`.
Silent mode hides the fault's identity in API responses until it is resolved.
