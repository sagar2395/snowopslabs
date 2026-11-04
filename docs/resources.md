# Resources

How much CPU and memory the lab needs, what each scenario adds, and how to give
Docker more.

## The short version

| Docker size | What it runs |
|---|---|
| **2 CPU / 4 GB** (minimum) | The cluster, the monitoring stack and **any one scenario** at a time. |
| **4 CPU / 6 GB** | Two scenarios together, such as observability plus autoscaling, or Kafka plus anything light. |
| **4 CPU / 8 GB** | Several scenarios at once, or the heavy ones together. |

`labctl init` refuses to build the lab on less than 2 CPU / 4 GB. Before any
scenario or fault starts, labctl checks the lab has room for it (see below), so
you never need to work these numbers out yourself.

## What each scenario needs on its own

Docker memory a fresh lab needs to run the scenario alone, with the app it runs
against:

| Scenario | Docker memory | Notes |
|---|---|---|
| backup-restore-drill | 2.2 GB | |
| env-promotion | 2.4 GB | |
| cost-right-sizing | 2.4 GB | OpenCost; on 2 CPUs the inflated request shrinks to what one node can schedule |
| autoscaling-under-load | 2.7 GB | KEDA |
| secrets-management | 2.7 GB | Vault, External Secrets |
| mesh-traffic-management | 2.8 GB | Istio or Linkerd; best with 4 CPUs |
| cluster-upgrade-drill | 2.9 GB | adds a second agent node while it runs; runs alone |
| node-drain-drill | 3.0 GB | adds a second agent node while it runs |
| security-compliance | 3.1 GB | Kyverno, cert-manager |
| chaos-engineering | 3.2 GB | Chaos Mesh, plus the stress it applies |
| gitops-cicd | 3.2 GB | Argo CD |
| observability-sre | 3.3 GB | Loki, Promtail, Tempo, Alloy |
| event-driven-arch | 3.6 GB | Kafka |

The lab itself (server node, one agent, Traefik, Prometheus, Grafana) is about
1.9 GB of that.

## Running several at once

Scenarios share what they can. Two scenarios that both use Loki or the same
app count it once, so a second scenario usually adds much less than its figure
above. When you start one, labctl estimates the lab's memory two ways and uses
the larger:

- the baseline plus the footprint of everything active and the new scenario,
  from [`config/footprints.yaml`](../config/footprints.yaml) and each
  scenario's `requirements`; and
- the memory in use on the machine that runs the cluster (the Docker VM, or
  the host on native Linux: total minus available in `/proc/meminfo`, so
  reclaimable cache does not count) plus what the new scenario would start
  that is not already running.

The lab may plan to use 85% of Docker's memory; the rest absorbs start-up spikes.
Over that, the scenario does not start, and you are told what would make room.
On a 4 GB Docker with observability-sre running, starting event-driven-arch
prints:

```
Not enough memory for event-driven-arch: the lab would need about 4 GB and may use 3.2 GB (85% of Docker's 3.8 GB, keeping room for spikes).
Either free memory:
  labctl scenario down observability-sre
  labctl platform down logging/loki
  labctl platform down tracing/tempo
or give Docker 5 GB:
  colima stop && colima start --cpu 2 --memory 5
```

`scenario down` removes what the scenario deployed but leaves the platform
components it needed (Loki, Tempo, Kafka, …) running, because other scenarios
use them too. The advice therefore names the `labctl platform down` commands for
the components only that scenario used, and for any installed component nothing
active uses.

A scenario that runs best with more CPUs starts anyway, with a warning. Two
scenarios bound to the same app also start, with a warning that their changes
and grading can interfere; bind one to another app with `--app`.

A drill that needs a second agent node gets one while it runs; `scenario down`
removes it once no active scenario needs it. The dashboard's Docker card shows
how much of the memory the lab may use is taken.

## Giving Docker more

| Setup | How |
|---|---|
| colima (macOS) | `colima stop && colima start --cpu 4 --memory 8`, then `labctl init`. To have `init` start it at that size next time, set `LAB_CPUS` and `LAB_MEMORY` in `.env` in your clone. |
| Docker Desktop (macOS) | Settings → Resources → CPUs and Memory → Apply & Restart. |
| WSL2 (Docker Desktop or Docker Engine) | In `%UserProfile%\.wslconfig` set `memory=` and `processors=` under `[wsl2]`, then `wsl --shutdown` in PowerShell. |
| Linux | Docker uses the machine's own memory; close memory-hungry programs. |

`labctl doctor` shows what Docker has now.

## Disk

About 10 GB: the colima VM or Docker's storage, the cluster images and the
platform's volumes. `labctl teardown` removes the cluster; `colima delete
--data` removes colima's VM and its images.
