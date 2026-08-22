# Learning Paths

Learning paths are guided, ordered tracks that walk you through real lab
exercises — cluster setup, app deployment, observability, and incident
response — with machine-verifiable completion checks at every step.

## Directory layout

```
learn/
  <path-name>/
    path.yaml          # path metadata + ordered module list
    intros/            # per-module markdown intro files (optional)
    checks/            # per-module shell scripts for completion checks (optional)
```

## path.yaml schema

```yaml
name: kubernetes-foundations     # must match directory name
displayName: "Kubernetes Foundations"
description: "A short description shown in `labctl learn list`."
tags: [kubernetes, observability]
estimatedMinutes: 45             # informational

modules:
  - name: init-cluster           # unique within the path
    displayName: "Start the cluster"
    intro: intros/01-init-cluster.md   # optional; relative path
    action:
      type: command              # command | scenario | incident
      ref: "labctl runtime up"   # what the learner must do
    check:
      name: cluster-reachable
      type: script               # http | script | promql
      script: checks/cluster-reachable.sh
      timeoutSeconds: 30
```

### action.type values

| type | ref means |
|------|-----------|
| `command` | A shell command for the learner to run (shown verbatim) |
| `scenario` | A scenario name; the learner runs `labctl scenario up <ref>` |
| `incident` | A fault name; the learner runs `labctl incident inject <ref>` and resolves it |

### check.type values

| type | fields |
|------|--------|
| `http` | `url`, `expectStatus` — HTTP GET must return the given status (`expectedStatus` is a deprecated alias) |
| `script` | `script` — bash script relative to the path dir; exit 0 = pass |
| `promql` | `query`, `operator`, `value` — queries Prometheus |

## Authoring a new path

1. Create `learn/<your-path-name>/path.yaml`.
2. Add intro markdown files in `intros/` (optional but recommended).
3. Add completion-check scripts in `checks/` (for `type: script` checks).
4. Run `labctl learn list` — if the path appears, the schema is valid.

The schema is validated by `go test ./internal/learn/...` in CI — a broken
`path.yaml` fails the build before it reaches the cluster.

## Rules for path authors

1. Checks must be idempotent and exit 0 only when the module's objective is
   truly complete (not just "the lab is up").
2. Intro files must live inside the path directory — no `..` or absolute paths.
3. Action refs must name real scenarios or faults (names validated at load time
   by the scenario/incident engines when the module is run).
4. Keep paths self-contained: do not depend on external URLs or credentials
   that won't be present on every lab machine.
