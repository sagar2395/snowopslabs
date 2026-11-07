# Fault library

Faults are reversible production failures that labctl injects into your lab
while traffic flows. Each one starts the way a page does: with a symptom, not
a cause. You diagnose it with the usual tools (`kubectl`, Grafana, logs and
traces), fix it the way you would in production, and its detection check
tells you whether the fix holds. Time to resolve and hints used are recorded.

- **Running one?** The commands and flags are in the
  [CLI reference](reference/cli/incidents.md).
- **Writing one?** The file contract and the rules for fault authors are in
  [incidents/README.md](../incidents/README.md).

## Run a fault

```bash
labctl incident list                                         # every fault, by category and severity
labctl incident inject crashloop-bad-config --deploy-prereqs # break the lab
labctl incident status                                       # resolved yet?
labctl incident hint                                         # the next hint, one at a time
labctl incident resolve                                      # the escape hatch: put the lab back
```

For a game day, inject a fault without naming it:

```bash
labctl incident inject --random --silent      # a surprise fault, name hidden
labctl incident inject --random --seed 42     # the same pick for everyone on the team
```

[Running a game day](game-day.md) covers a whole team session on one shared
lab: roles, the day before, rounds, rescuing a round and the review.

## The faults

What you see when you start. The cause is what you have to find, so it is not
listed here; `labctl incident solution <name>` has the full walkthrough when
you want it.

### Workload

| Fault | Severity | What you walk into |
|---|---|---|
| `crashloop-bad-config` | high | The app went down minutes after a config change. Every pod starts, dies within a second and starts again, and no old pod is left to serve. |
| `bad-deploy-rollout` | medium | A release is stuck. New pods never start while the old version keeps serving. |
| `liveness-probe-kill` | high | The app restarts every 40 seconds or so and users get bursts of errors. Every restart looks clean, with no crash and no OOM kill. |

### Network

| Fault | Severity | What you walk into |
|---|---|---|
| `network-blackhole` | high | Every request through the ingress fails, yet the pods are Running and Ready, the logs are quiet and nothing was deployed. |
| `stray-pod-behind-service` | high | A steady fraction of requests fails, not all of them. Every pod is Ready and the app's own error panel is flat. |
| `dns-egress-blocked` | high | After a security hardening, most requests fail with 502. `/health` answers, every pod is Ready, and the dependency the app calls is healthy from anywhere else. |

### Resources

| Fault | Severity | What you walk into |
|---|---|---|
| `oom-kill` | high | The app came up clean, served for a minute, then the restart count started climbing. Nothing was deployed. |
| `cpu-throttling` | medium | p99 latency is in seconds while the median looks fine. No restarts, no errors, and the node is nearly idle. |
| `pending-unschedulable` | medium | The app was scaled to 4 for a launch and READY has said 3/4 ever since, on mostly idle nodes. |
| `noisy-neighbor` | low | The node the app runs on is out of CPU. Nothing you deployed changed, and the other nodes are idle. |

### Config

| Fault | Severity | What you walk into |
|---|---|---|
| `service-selector-broken` | medium | The Service has no ready endpoints and every request is a 503, but every pod is Running and Ready. |
| `missing-secret-key` | high | After a credential rotation and a node rotation, no pod has started. There are no logs and no restarts. |

## Challenges built on them

A [challenge](../challenges/README.md) injects one or more of these faults,
starts a clock and grades your fix. The score starts at 100, loses points for
hints and for time over par, and scales with the checks you pass.

| Challenge | Par time | What it sets up |
|---|---|---|
| `restore-broken-deploy` | 6m | `bad-deploy-rollout` |
| `make-the-slo-green` | 6m | `stray-pod-behind-service` |
| `find-the-memory-leak` | 8m | `oom-kill` |
| `on-call-shift` | 12m | Two faults at once, unnamed: `missing-secret-key` and `liveness-probe-kill` |

```bash
labctl challenge list
labctl challenge start restore-broken-deploy
labctl challenge submit
```

The [kubernetes-foundations](../learn/README.md) learning path ends with a
fault you diagnose and fix by hand, after building the lab, deploying an app
and turning on observability.
