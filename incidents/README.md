# Incidents: Fault Library

Realistic, **reversible** production faults for game days, on-call practice
and the timed [challenges](../challenges/README.md). Inject one, watch the lab break, practice
diagnosing it, fix it, and let the detection check confirm you actually
fixed it. The commands are in the
[CLI reference](../docs/reference/cli/incidents.md), and the learner-facing
catalog is the [fault library](../docs/incidents.md).

## The contract

Every fault is a directory `incidents/<name>/` with:

| File | Purpose |
|------|---------|
| `fault.yaml` | Metadata + the **detection check** (passes ⇔ the fault is RESOLVED) |
| `inject.sh` | Breaks the lab. Idempotent: re-running while injected is a no-op. |
| `resolve.sh` | The escape hatch. Always restores the lab, even after a partial manual fix. Also runs after a hand fix passes `status`, to remove the alert rule and the fault's record, so it must change nothing that is already fixed. |
| `hints.md` | Progressive hints, one `## Hint N` section each (revealed by `labctl incident hint`) |
| `solution.md` | Full diagnosis + fix walkthrough (spoiler - `labctl incident solution`) |
| `alerts/rule.yaml` | Required iff `expectAlert` is set: the PrometheusRule that fires the page (armed by `inject.sh`, disarmed by `resolve.sh`) |
| `manifests/`, `checks/` | Optional supporting files |

Every fault also has tests in `src/test/shell/`: its detection check in
`incident_checks.bats` and its `inject.sh` and `resolve.sh` in
`incident_faults.bats`. See [Testing a fault](#testing-a-fault).

### fault.yaml

```yaml
name: crashloop-bad-config        # must match the directory name
verified: true                    # optional: confirmed end-to-end on a fresh cluster (else unverified)
displayName: "CrashLoop: a bad config value takes the app down"
description: "What the victim experiences, not how it's injected"
category: workload                # workload | network | resources | storage | config
severity: high                    # low | medium | high
target:                           # templatable - see "Targeting a workload"
  namespace: "{{.WorkloadNamespace}}"
  workload: "{{.WorkloadName}}"
prerequisites:
  apps: ["{{.WorkloadName}}"]     # the bound workload must exist; gated before injection
detection:                        # same schema as scenario checks
  name: rollout-healthy           # PASSES when the fault is RESOLVED
  type: script                    # http | kubectl | promql | script
  script: checks/resolved.sh
  timeoutSeconds: 30
expectAlert: LabFaultCrashLoop    # optional: this alert should page

references:                       # optional: upstream docs for the fix
  - label: "Kubernetes - Debug a CrashLooping pod"
    url: "https://kubernetes.io/docs/tasks/debug/debug-application/"
    note: "Optional context."
snippets:                         # optional: applyable diagnose/remediate manifests
  - label: "Restore the PORT setting"
    description: "Strategic-merge patch that undoes the fault."
    yaml: |                       # inline manifest, OR `path: manifests/fix.yaml`
      spec:
        template:
          spec:
            containers:
              - name: go-api
                env:
                  - name: PORT
                    value: "8080"
```

### Targeting a workload

`target` names what the fault breaks. The engine exports it to `inject.sh`,
`resolve.sh` and the detection check as `TARGET_NAMESPACE` and `TARGET_WORKLOAD`,
which every script reads instead of hardcoding a name:

```sh
NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
```

`DOMAIN_SUFFIX` and `MONITORING_NAMESPACE` travel with them, to all three. A
detection check needs the suffix as much as inject and resolve do, the checks
that matter probe the workload through its own ingress, and a script check runs
on the checks runner, which does not inherit the executor's environment.

Both fields are template-resolved. A fault whose target reads
`{{.WorkloadNamespace}}` / `{{.WorkloadName}}` follows whatever app the lab is
bound to, so the same fault can be injected against a built-in app or a user's
own (see [ADR-0014](../docs/adr/0014-workload-binding-and-app-contract.md)). A
fault that pins a literal keeps breaking exactly the workload it names - correct
when the fault only makes sense for that one app.

### Scripts, and the shared library

`inject.sh`, `resolve.sh` and the detection check are executed as files, so the
engine does not template them. They read the target from the environment:

```sh
NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
```

A fault's own manifests (an alert rule, a NetworkPolicy) are applied by the
script rather than by the engine, so they carry shell-style placeholders and are
rendered through the shared helper before `kubectl` sees them:

```sh
# shellcheck source=/dev/null
. "$(cd "$SCRIPT_DIR/../_lib" && pwd)/render.sh"
render_targeted "$SCRIPT_DIR/manifests/policy.yaml" | kubectl apply -f -
```

An alert rule goes through `render_rule` instead. It renders the same way, and
in a `--silent` inject (`FAULT_SILENT` set) it also renames the PrometheusRule
and its group to `labfault-silent`, because Prometheus' Alerts page shows both
and the fault's name would give the drill away. `resolve.sh` deletes the rule
under both names:

```sh
render_rule "$SCRIPT_DIR/alerts/rule.yaml" | kubectl apply -n "$MON_NS" -f -
kubectl delete prometheusrule "labfault-$FAULT" labfault-silent -n "$MON_NS" --ignore-not-found
```

```yaml
expr: max(kube_pod_container_status_last_terminated_reason{namespace="${TARGET_NAMESPACE}"}) > 0
```

Directories under `incidents/` beginning with `_` are not faults - `_lib` holds
these shared helpers.

**Calibrate against the binding, not a fixed app.** A fault that hardcodes an
absolute number is wrong for the next application: `oom-kill` derives its memory
limit from the workload's own request rather than naming 8Mi, because a value
that reproduces the failure for a small Go service would stop a JVM from ever
starting.

`references` and `snippets` use the same shape as scenarios (see
[scenario schema → References and snippets](../docs/reference/scenario-schema.md#references-and-snippets)):
a reference is `{label, url, note?}`; a snippet is `{label, description?, yaml |
path, exercise?, apply?}` with exactly one of `yaml`/`path` (a `path` is relative
to the incident directory). Both are template-resolved for the app the fault is
injected into and shown by `labctl incident info <name>`, with long comment
blocks trimmed from the body. `labctl validate` fails on a dangling snippet `path`.

### Paging (`expectAlert`, on-call drills)

A fault that sets `expectAlert` must ship `alerts/rule.yaml`, a
PrometheusRule labeled `release: prometheus` (so kube-prometheus-stack
loads it) whose alert carries `labfault: "true"` (so Alertmanager routes it
to the pager, see `platform/monitoring/metrics/prometheus/values.yaml`).
`inject.sh` arms the rule (tolerating a missing monitoring stack),
`resolve.sh` disarms it, and `labctl incident status` reports whether the
page fired by querying the Alertmanager API
(`ALERTMANAGER_URL`, default `http://alertmanager.<DOMAIN_SUFFIX>`).
Pages land in the in-cluster **pager** (`labctl service up pager`) unless
`ALERT_WEBHOOK_URL` points Alertmanager somewhere else.

## Rules for fault authors

1. **Target only the demo apps** (`go-api`, `echo-server`) or a dedicated
   fault namespace, never platform components or `kube-system`.
2. **Record what you change, away from what you break.** Save the original
   values with `fault_state_save` from `incidents/_lib/state.sh`; `resolve.sh`
   reads them with `fault_state_get` and ends with `fault_state_clear`. The
   record is a ConfigMap `labfault-<name>` in `kube-system`. Never annotate or
   label the object the fault breaks: the original value would be on screen in
   the learner's first `kubectl describe`.
3. **`resolve.sh` must never fail the user.** It runs after any amount of
   manual fixing; every step tolerates "already fixed" (`--ignore-not-found`,
   guards, `|| true` where safe).
4. **Portable shell** (CONTRIBUTING.md golden rules 1) - these scripts are
   shellcheck'd and portability-linted in CI.
5. **Write the detection check as "what must be true when healthy"**: it
   doubles as the resolution detector and the challenge grader.
6. Hints go from gentle nudge to near-answer; the last hint may name the
   resource, the solution names the command.
7. **Nothing `inject.sh` prints names the fault**, and nothing it writes on the
   learner's objects does either. An object the fault adds to the workload's
   namespace takes the name it would have in a real cluster
   (`default-deny-ingress`, not `labfault-…`) and no `labfault` label;
   `resolve.sh` finds it again through the fault's record.

## Testing a fault

The tests run the real scripts against a scripted `kubectl` (`stub_script` in
`src/test/shell/helpers/stub.bash`), so they need no cluster.

**The detection check** gets one test per lab state in `incident_checks.bats`:

| State | The check must |
|---|---|
| Solved | pass |
| The fault is in | fail, with a message that says what is wrong |
| Each wrong fix that hides the symptom (scaled to zero, the symptom relabelled away) | fail |
| The workload or a tool is missing | fail with a reason, never pass and never exit silently |

**`inject.sh` and `resolve.sh`** join the tables at the top of
`incident_faults.bats`, which assert for every fault that inject makes its
change, fails when that change fails, records before it changes anything,
carries on without the alert rule, and gives nothing away in its output or on
the workload's objects. Add tests of the fault's own for: a second inject
changes nothing; resolve restores each recorded case; resolve after a hand fix
leaves the learner's fix alone; resolve with no record still recovers or says
how.

Run them with `bats src/test/shell/incident_checks.bats
src/test/shell/incident_faults.bats`. They are the hermetic half; the
[incident review](../docs/authoring/incident-review.md) on a live lab is the
other.

**`test/solve.sh`** is the hand fix from `solution.md` as a script, run by the
nightly cluster suite after a real inject; the detection check must then pass
([TESTING](../docs/TESTING.md#real-cluster)). It uses `kubectl` the way a
learner would, never `resolve.sh`. Try it on your own lab with
`make test-cluster ITEM=incident/<name>`.

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
| `crashloop-bad-config` | workload | high | a config change sets the workload's `PORT` to a port that cannot exist and no old pod is left serving - every pod crash-loops with the error in its logs, and every request fails |
| `bad-deploy-rollout` | workload | medium | the workload is "deployed" with a tag that was never pushed - rollout sticks in ImagePullBackOff |
| `oom-kill` | resources | high | the workload's memory limit is cut just below what it needs under load, and k6 traffic is started - the pod idles fine and is OOMKilled once requests arrive |
| `network-blackhole` | network | high | a deny-all-ingress NetworkPolicy lands in the workload's namespace - the service goes dark through the ingress |
| `service-selector-broken` | config | medium | the workload's Service selector stops matching its pods - endpoints empty, pods perfectly healthy (sneaky) |
| `stray-pod-behind-service` | network | high | a hand-run, Ready pod copies the Service labels but calls a dead upstream - endpoints have one extra address and a steady fraction of requests burn the SLO |
| `noisy-neighbor` | resources | low | a batch tenant lands on the workload's node with big CPU requests and no limits, and takes the machine |
| `pending-unschedulable` | resources | medium | a launch scales the workload to 4 and raises its CPU request past any node's allocatable CPU, so the new replicas stay Pending (`Insufficient cpu`) while the old ones serve; nothing crashes |
| `liveness-probe-kill` | workload | high | a values update points the liveness probe at `/healthz`, a path the app does not serve; the kubelet kills a healthy app every 40 seconds or so (exit 0, `Liveness probe failed: ... statuscode: 404`) and k6 traffic sees bursts of 5xx |
| `missing-secret-key` | config | high | a credential rotation renames the key the workload reads from its Secret, then a node rotation replaces the pods; none can start (`CreateContainerConfigError`, `couldn't find key ... in Secret`), a full outage with no logs |
| `cpu-throttling` | resources | medium | the CPU limit is cut to a third of what the workload uses under the steady k6 load it starts; no restarts, no errors, every pod Ready, but `kubectl top` pins at the limit, throttling climbs and p99 latency goes to seconds |
| `dns-egress-blocked` | network | high | a default-deny egress policy added for security allows the upstream's port but not DNS; inbound traffic and `/health` work, every pod is Ready, and requests that call the upstream fail with a failed lookup. Unlike `network-blackhole`, the requests arrive |

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
