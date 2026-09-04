# Learning & challenges

## `labctl learn` — guided paths

Learning paths combine cluster setup, app deployment, scenarios and incidents
into structured modules with machine-verifiable completion checks. Progress
lives in `.labctl/learn/` and survives CLI restarts.

```bash
labctl learn list                                    # paths with your progress
labctl learn start kubernetes-foundations            # start or restart a path
labctl learn next kubernetes-foundations             # verify the current module and advance
labctl learn next kubernetes-foundations --show-only # print the intro without checking
labctl learn progress                                # all paths
labctl learn progress kubernetes-foundations
```

`next` shows the next incomplete module's intro and objective, then runs its
completion check. Run it again once you have done the task to verify and
advance. If only one path exists, the path argument can be omitted.

## `labctl challenge` — timed assessments

A challenge injects a real fault (or activates a scenario), races you against a
par time, and auto-grades on submit. Each hint costs score.

```bash
labctl challenge list
labctl challenge info restore-broken-deploy
labctl challenge start restore-broken-deploy    # sets up and starts the timer
labctl challenge status                         # elapsed time and hints used
labctl challenge hint                           # next hint (−5% score each)
labctl challenge submit                         # run checks, score, record the result
labctl challenge abort                          # undo setup, score as aborted
labctl challenge history                        # past runs with MTTR, score, hints
```

`start` flags:

| Flag | Default | Meaning |
|---|---|---|
| `--deploy-prereqs` | off | build and deploy the app the challenge needs |
| `--force` | off | override an already-active challenge |

**Score formula:** `100 − (hints × penalty) − time_over_par_penalty`, scaled by
the fraction of checks that pass. Details in
[challenges/README.md](../../../challenges/README.md).

REST: `GET /api/v2/challenges`, `GET /api/v2/challenges/{name}`,
`GET /api/v2/challenges/status`, `GET /api/v2/challenges/history`,
`POST /api/v2/challenges/complete`.
