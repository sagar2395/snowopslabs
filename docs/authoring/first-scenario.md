# Your First Scenario

This walks the full loop — **scaffold → edit → verify → share** — in a few
minutes. It is the fast path for a first contribution.

> Prereqs: `src/bin/labctl` built (`make cli-build`) and a running lab
> (`labctl lab up`). For inline editor validation: VS Code + the Red Hat YAML
> extension — the repo's `.vscode/settings.json` wires the schemas up for you.

## 1. Scaffold

A **scenario** is one declarative playground: a directory containing a
`scenario.yaml` plus its assets (manifests, Helm values, dashboards, scripts).

```bash
labctl scenario new my-first-scenario     # -> scenarios/my-first-scenario/
```

The scaffold is **valid and verify-ready out of the box**: a v2 `scenario.yaml`
and a passing `checks/ready.sh`. The YAML carries a
`# yaml-language-server: $schema=…` modeline, so your editor validates as you
type — even outside this repo.

## 2. Edit

Open `scenario.yaml` and make it real:

- **`description`, `objectives`** — what the learner does and what they take
  away. Write these for a human who has never seen the scenario. This text is
  what the UI shows before anyone commits to a 10-minute install.
- **`stages[].components`** — what to deploy, in order. Component types are
  `helm`, `manifest`, `grafana-dashboard` and `script`. Full reference:
  [scenarios.md](../scenarios.md).
- **`checks`** — machine-verifiable assertions. This is the important part.
  Replace the scaffolded `script` check with real `http`, `kubectl`, `promql`
  or `script` checks. A scenario without meaningful checks cannot be graded,
  cannot gate a learning path, and cannot be marked verified.

Good checks assert the *outcome the objective describes*, not that a pod exists.
"p99 latency below 300ms" is a check; "deployment is present" is a tautology.

## 3. Validate

Catch mistakes before touching a cluster:

```bash
labctl validate                          # schema + cross-reference integrity
labctl scenario info my-first-scenario   # parses and renders stages/checks
```

`labctl validate` is the same gate CI runs, so if it passes locally your PR
will not fail on content errors.

## 4. Verify

Run the checks against a live cluster:

```bash
labctl scenario up my-first-scenario       # activate it
labctl scenario verify my-first-scenario   # run the checks; --watch to retry
labctl scenario down my-first-scenario     # clean up
```

`verify` is green immediately on the scaffold. Keep it green as you add real
components and checks — that discipline is what makes the scenario trustworthy.

## 5. Share it

Scenarios are directories, so sharing is git. Two options:

**Contribute it here.** Open a PR adding `scenarios/my-first-scenario/`. If you
want the `verified` badge, the scenario must pass the nightly kind e2e job —
see [../TESTING.md](../TESTING.md).

**Keep it private.** Put the directory in your own repository and point
SnowOps Labs at it:

```bash
export SNOWOPS_CONTENT_PATH=/path/to/my-content-repo
labctl scenario list        # your scenario appears, badged as external
```

No registry, no publishing step, no signing. See
[../adr/0008-content-extensibility-seam.md](../adr/0008-content-extensibility-seam.md)
for why it works this way.

> ⚠️ Content from an external root runs shell scripts against your cluster with
> your credentials. Only point `SNOWOPS_CONTENT_PATH` at sources you trust.

## Contributing back

- Good first issues are labelled **`good first issue`** / **`help wanted`** (see
  [`.github/labels.yml`](../../.github/labels.yml)).
- Read [`CONTRIBUTING.md`](../../CONTRIBUTING.md); content PRs must be
  cross-platform, idempotent and declarative (the golden rules).
