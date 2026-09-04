# Incidents — Fault Library

Realistic, **reversible** production faults for game days, on-call practice,
and (later) graded challenges. Inject one, watch the lab break, practice
diagnosing it, fix it — and let the detection check confirm you actually
fixed it. The commands are in the
[CLI reference](../docs/reference/cli/incidents.md).

## The contract

Every fault is a directory `incidents/<name>/` with:

| File | Purpose |
|------|---------|
| `fault.yaml` | Metadata + the **detection check** (passes ⇔ the fault is RESOLVED) |
| `inject.sh` | Breaks the lab. Idempotent: re-running while injected is a no-op. |
| `resolve.sh` | The escape hatch. Always restores the lab, even after a partial manual fix. |
| `hints.md` | Progressive hints, one `## Hint N` section each (revealed by `labctl incident hint`) |
| `solution.md` | Full diagnosis + fix walkthrough (spoiler — `labctl incident solution`) |
| `alerts/rule.yaml` | Required iff `expectAlert` is set: the PrometheusRule that fires the page (armed by `inject.sh`, disarmed by `resolve.sh`) |
| `manifests/`, `checks/` | Optional supporting files |

### fault.yaml

```yaml
name: crashloop-bad-config        # must match the directory name
verified: true                    # optional: confirmed end-to-end on a fresh cluster (else unverified)
displayName: "CrashLoop: broken container command"
description: "What the victim experiences, not how it's injected"
category: workload                # workload | network | resources | storage | config
severity: medium                  # low | medium | high
target:
  namespace: go-api
  workload: go-api
prerequisites:
  apps: [go-api]                  # gated before injection
detection:                        # same schema as scenario checks
  name: rollout-healthy           # PASSES when the fault is RESOLVED
  type: script                    # http | kubectl | promql | script
  script: checks/resolved.sh
  timeoutSeconds: 30
expectAlert: LabFaultCrashLoop    # optional: this alert should page

references:                       # optional: upstream docs for the fix
  - label: "Kubernetes — Debug a CrashLooping pod"
    url: "https://kubernetes.io/docs/tasks/debug/debug-application/"
    note: "Optional context."
snippets:                         # optional: applyable diagnose/remediate manifests
  - label: "Restore the container command"
    description: "Strategic-merge patch that undoes the fault."
    yaml: |                       # inline manifest, OR `path: manifests/fix.yaml`
      spec:
        template:
          spec:
            containers:
              - name: go-api
                command: null
```

`references` and `snippets` use the same shape as scenarios (see
[scenario schema → References and snippets](../docs/reference/scenario-schema.md#references-and-snippets)):
a reference is `{label, url, note?}`; a snippet is `{label, description?, yaml |
path}` with exactly one of `yaml`/`path` (a `path` is relative to the incident
directory). Both are template-resolved and shown by `labctl incident info
<name>`. `labctl validate` fails on a dangling snippet `path`.

### Paging (`expectAlert`, on-call drills)

A fault that sets `expectAlert` must ship `alerts/rule.yaml` — a
PrometheusRule labeled `release: prometheus` (so kube-prometheus-stack
loads it) whose alert carries `labfault: "true"` (so Alertmanager routes it
to the pager — see `platform/monitoring/metrics/prometheus/values.yaml`).
`inject.sh` arms the rule (tolerating a missing monitoring stack),
`resolve.sh` disarms it, and `labctl incident status` reports whether the
page fired by querying the Alertmanager API
(`ALERTMANAGER_URL`, default `http://alertmanager.<DOMAIN_SUFFIX>`).
Pages land in the in-cluster **pager** (`labctl service up pager`) unless
`ALERT_WEBHOOK_URL` points Alertmanager somewhere else.

## Rules for fault authors

1. **Target only the demo apps** (`go-api`, `echo-server`) or a dedicated
   fault namespace — never platform components or `kube-system`.
2. **Record what you change.** Annotate the touched resource
   (`labfault-<name>=...`, original values in `labfault-<name>-original-*`)
   so `resolve.sh` can always undo it without guessing.
3. **`resolve.sh` must never fail the user.** It runs after any amount of
   manual fixing; every step tolerates "already fixed" (`--ignore-not-found`,
   guards, `|| true` where safe).
4. **Portable shell** (CONTRIBUTING.md golden rules 1) — these scripts are
   shellcheck'd and portability-linted in CI.
5. **Write the detection check as "what must be true when healthy"** — it
   doubles as the resolution detector and the challenge grader.
6. Hints go from gentle nudge to near-answer; the last hint may name the
   resource, the solution names the command.

## Reviewing a fault

`labctl validate` proves a fault is well-formed and `resolve` proves it is
reversible. Neither proves it is a good drill. The
[incident review](../docs/authoring/incident-review.md) workflow does: it
injects the fault on a live lab, diagnoses it blind, walks the hint ladder,
false-fix tests the detection check in both directions, exercises the escape
hatch from every state a learner can leave behind, and scores the result out of
5. Below 4.8, the fault is still a draft.

## Current faults

| Fault | Category | Severity | What breaks |
|-------|----------|----------|-------------|
| `crashloop-bad-config` | workload | medium | go-api's container command is replaced with one that exits immediately — new pods crash-loop |
| `bad-deploy-rollout` | workload | medium | go-api is "deployed" with a nonexistent image tag — rollout sticks in ImagePullBackOff |
| `oom-kill` | resources | high | echo-server's memory limit is cut just below what it needs under load, and k6 traffic is started — the pod idles fine and is OOMKilled once requests arrive |
| `network-blackhole` | network | high | a deny-all-ingress NetworkPolicy lands in go-api's namespace — the service goes dark through the ingress |
| `service-selector-broken` | config | medium | go-api's Service selector stops matching its pods — endpoints empty, pods perfectly healthy (sneaky) |
| `noisy-neighbor` | resources | low | a CPU-burning deployment lands on the cluster with big requests and no limits |

`dns-blackhole` and `pvc-full` were considered and dropped: DNS exec probes
and PVC behaviour vary too much with the local storage and CNI setup for
detection to be reliable on a default k3d cluster. `network-blackhole` and
`service-selector-broken` cover the same ground dependably.

## Using faults

```bash
labctl incident list
labctl incident inject service-selector-broken     # or --random [--seed N] [--silent]
labctl incident status                             # runs the detection check
labctl incident hint                               # next hint (recorded)
labctl incident resolve                            # escape hatch
```
