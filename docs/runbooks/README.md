# Runbooks

Runbooks are the **human validation gate**. Automated tests prove the code does
what we told it to; a runbook proves the product works on real hardware, with a
real cluster, for a real person.

**No wave merges until its runbooks pass.** That is the contract.

---

## How to use these

Each runbook is written to be followed top to bottom by a person at a terminal.
You should never need to consult the source to complete one.

- **Preconditions** are stated up front: what must be installed, how much disk
  and RAM, roughly how long it takes.
- **Every step states its expected output.** If what you see differs, that is a
  finding; note it and continue where it is safe to.
- **Failure signatures** are listed for the mistakes we expect people to hit, so
  a wrong turn is diagnosable rather than mysterious.
- **Teardown** always ends the runbook. A runbook that leaves a cluster running
  is a bug in the runbook.
- **A results table** at the end is filled in and reported back: pass, fail, or
  deviation per step.

Report findings as GitHub issues labelled `runbook-finding` with the runbook ID
and step number. A failing step blocks the wave.

## Conventions

| Symbol | Meaning |
|---|---|
| `$` | Run in your shell |
| **Expect:** | What success looks like |
| ⚠️ | A step that mutates cluster state or costs real time |
| 🔍 | An observation step - read something, do not change anything |

Runbooks assume the golden path: macOS or Linux, Docker running, `k3d`
available. Where a step differs between macOS and Linux, both are given.

## Which runbook do I need?

When troubleshooting a symptom or validating a specific subsystem, use this index to find the right runbook:

| Situation / Symptom | Runbook | What it validates |
|---|---|---|
| I want to contribute, build `labctl` from source, or run unit tests | [R00 - Environment & build](R00-environment-and-build.md) | Source compilation, toolchain prerequisites, test suite, and lint gates on a fresh clone. |
| An operation is stuck, needs cancelling, or the server was restarted mid-run | [R01 - Run engine & cancellation](R01-run-engine-and-cancellation.md) | Operation recording, CLI/UI cancellation, child process cleanup (`helm`), and restart recovery. |
| Tools are missing, outdated, or Docker is stopped / undersized | [R02 - Doctor & preflight](R02-doctor-and-preflight.md) | Preflight tool detection, version threshold enforcement, stopped Docker engine diagnostics, and fix instructions. |
| I am creating a custom scenario, editing scenario YAML, or testing external content | [R03 - Content authoring & validation](R03-content-authoring-and-validation.md) | Scenario schema validation, file/line error reporting, and loading external content via `SNOWOPS_CONTENT_PATH`. |
| The cluster fails to start, reboot recovery fails, or I need to test snapshot & restore | [R04 - Lab lifecycle reliability](R04-lab-lifecycle.md) | Idempotent cluster lifecycle, reboot recovery, snapshot save/restore, and clean teardown on k3d. |
| A platform component fails to install, Helm upgrades fail, or charts conflict | [R05 - Platform components](R05-platform-components.md) | Idempotent platform component installation, Helm upgrade strategies for immutable specs, and service health checks. |
| I want to practice manual image rollouts and promotion across dev, staging, and prod | [R06 - Multi-env promotion](R06-multi-env-promotion.md) | Multi-namespace delivery pipeline, manual image promotion via `kubectl`, and automated outcome grading. |
| Dashboards have no data, Loki logs are empty, or traces are missing | [R13 - Observability pipeline](R13-observability-pipeline.md) | End-to-end metrics, logs, and trace telemetry flow for `go-api`, plus Grafana, Loki, and Tempo correlation. |
| People need to reach one lab over the network | [R11 - Team server](R11-team-server.md) | Auth and HTTPS on a network bind, three logins, sessions across a restart, and a participant kubeconfig. |
| A first-time user is installing a release on fresh macOS or WSL2 | [R14 - Fresh-machine onboarding](R14-fresh-machine-onboarding.md) | First-time user onboarding on fresh macOS and WSL2 following only the public README. |

## Index

Runbooks are written as their wave is implemented.

| ID | Runbook | Wave | Status |
|---|---|---|---|
| R00 | [Environment & build](R00-environment-and-build.md) | W0 | **ready** |
| R01 | [Run engine & cancellation](R01-run-engine-and-cancellation.md) | W1 | **ready** |
| R02 | [Doctor & preflight](R02-doctor-and-preflight.md) | W1 | **ready** |
| R03 | [Content authoring & validation](R03-content-authoring-and-validation.md) | W2 | **ready** |
| R04 | [Lab lifecycle reliability](R04-lab-lifecycle.md) | W3 | **ready** (reliability slice) |
| R05 | [Platform components](R05-platform-components.md) | W3 | **ready** (durable service slice) |
| R06 | [Multi-env promotion](R06-multi-env-promotion.md) | W4 | **ready** |
| R07 | Game day & incidents | W4 | planned |
| R08 | API & security | W5 | planned |
| R09 | UI operational walkthrough | W6 | planned |
| R10 | Learning & assessment | W7 | planned |
| R11 | [Team server](R11-team-server.md) | W8 | **ready** |
| R12 | Release verification | W8 | planned |
| R13 | [Observability pipeline](R13-observability-pipeline.md) | W4 | **ready** |
| R14 | [Fresh-machine onboarding](R14-fresh-machine-onboarding.md) | W8 | **ready** |

## What each runbook will ask you to prove

Stated now so the intent is reviewable before the work happens.

**R00: Environment & build.** Clone fresh, build the binary on your machine,
run the full test suite, confirm every lint gate runs. Proves a new contributor
can get started.

**R01 - Run engine & cancellation.** Start a long platform install; cancel it
from the CLI and from the UI. Confirm no `helm` processes survive
(`pgrep -f helm`). Kill the server mid-run, restart, confirm the run shows
`cancelled` with its partial log readable. Fire two conflicting operations,
confirm the second is refused naming the first. Disconnect the log stream
mid-run and reconnect, confirm no missing lines.

**R02: Doctor & preflight.** Rename `helm` on your `PATH`, run `labctl doctor`,
confirm the error names the binary and tells you how to install it. Repeat with
an outdated version and with Docker stopped.

**R03: Content authoring & validation.** Scaffold a new scenario, break its
YAML in three specific ways, confirm each produces an error naming the file,
line and problem. Point `SNOWOPS_CONTENT_PATH` at a directory outside the
repo and confirm your scenario appears, badged as external.

**R04/R05 - Lab & platform.** Full lifecycle on k3d. Interrupt `platform up`
midway, re-run, confirm convergence. Confirm `lab down` removes everything and
`kubectl get ns` is clean.

**R06/R07 - Scenario & game day.** Run a verified scenario end to end. Fail its
checks deliberately and read the observed-vs-expected output. Inject an
incident, use a hint, fix it, confirm MTTR and score. Run the on-call drill and
confirm a page actually arrives.

**R08 - API & security.** Attempt to bind `0.0.0.0` without auth and confirm
refusal. Verify session survival across restart, CSRF rejection, and rate
limiting under a scripted login loop.

**R09: UI walkthrough.** Every view: deep-link, refresh, resize to mobile,
tab-navigate with the keyboard, toggle themes, kill the backend and watch it
recover. Judged on whether it is genuinely pleasant, not merely functional.

**R10 - Learning & assessment.** Complete a learning path entirely in the UI.
Run a challenge, confirm the score matches the stated rubric.

**R11: Team server.** Bring up the single-machine team server with auth and
HTTPS, log three users in from the network, keep their sessions across a
restart, and give participants a kubeconfig that verifies the cluster's
certificate.

**R13: Observability pipeline.** Prove metrics, logs and traces flow end to end
for `go-api`, that log lines link to their traces, that alert rules are actually
loaded, and that the scenario is idempotent over an existing platform install.
Each step names the silent failure it is guarding against.

**R14: Fresh-machine onboarding.** Install a release and build the lab as a new
user on macOS and WSL2, where CI cannot, following only the README: doctor on an
empty machine, init end to end, a first scenario, the capacity block, recovery
after a reboot, upgrade and uninstall.

**R12: Release verification.** Download the release artifacts, verify
checksums and the cosign signature, inspect the SBOM, run the binary on a
machine that has never seen the repo.
