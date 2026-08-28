# Runbooks

Runbooks are the **human validation gate**. Automated tests prove the code does
what we told it to; a runbook proves the product works on real hardware, with a
real cluster, for a real person.

**No wave merges until its runbooks pass.** That is the contract.

---

## How to use these

Each runbook is written to be followed top to bottom by a person at a terminal.
You should never need to consult the source to complete one.

- **Preconditions** are stated up front — what must be installed, how much disk
  and RAM, roughly how long it takes.
- **Every step states its expected output.** If what you see differs, that is a
  finding; note it and continue where it is safe to.
- **Failure signatures** are listed for the mistakes we expect people to hit, so
  a wrong turn is diagnosable rather than mysterious.
- **Teardown** always ends the runbook. A runbook that leaves a cluster running
  is a bug in the runbook.
- **A results table** at the end is filled in and reported back — pass, fail, or
  deviation per step.

Report findings as GitHub issues labelled `runbook-finding` with the runbook ID
and step number. A failing step blocks the wave.

## Conventions

| Symbol | Meaning |
|---|---|
| `$` | Run in your shell |
| **Expect:** | What success looks like |
| ⚠️ | A step that mutates cluster state or costs real time |
| 🔍 | An observation step — read something, do not change anything |

Runbooks assume the golden path: macOS or Linux, Docker running, `k3d`
available. Where a step differs between macOS and Linux, both are given.

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
| R06 | Scenario loop | W4 | planned |
| R07 | Game day & incidents | W4 | planned |
| R08 | API & security | W5 | planned |
| R09 | UI operational walkthrough | W6 | planned |
| R10 | Learning & assessment | W7 | planned |
| R11 | Team server | W8 | planned |
| R12 | Release verification | W8 | planned |

## What each runbook will ask you to prove

Stated now so the intent is reviewable before the work happens.

**R00 — Environment & build.** Clone fresh, build the binary on your machine,
run the full test suite, confirm every lint gate runs. Proves a new contributor
can get started.

**R01 — Run engine & cancellation.** Start a long platform install; cancel it
from the CLI and from the UI. Confirm no `helm` processes survive
(`pgrep -f helm`). Kill the server mid-run, restart, confirm the run shows
`cancelled` with its partial log readable. Fire two conflicting operations,
confirm the second is refused naming the first. Disconnect the log stream
mid-run and reconnect, confirm no missing lines.

**R02 — Doctor & preflight.** Rename `helm` on your `PATH`, run `labctl doctor`,
confirm the error names the binary and tells you how to install it. Repeat with
an outdated version and with Docker stopped.

**R03 — Content authoring & validation.** Scaffold a new scenario, break its
YAML in three specific ways, confirm each produces an error naming the file,
line and problem. Point `SNOWOPS_CONTENT_PATH` at a directory outside the
repo and confirm your scenario appears, badged as external.

**R04/R05 — Lab & platform.** Full lifecycle on k3d. Interrupt `platform up`
midway, re-run, confirm convergence. Confirm `lab down` removes everything and
`kubectl get ns` is clean.

**R06/R07 — Scenario & game day.** Run a verified scenario end to end. Fail its
checks deliberately and read the observed-vs-expected output. Inject an
incident, use a hint, fix it, confirm MTTR and score. Run the on-call drill and
confirm a page actually arrives.

**R08 — API & security.** Attempt to bind `0.0.0.0` without auth and confirm
refusal. Verify session survival across restart, CSRF rejection, and rate
limiting under a scripted login loop.

**R09 — UI walkthrough.** Every view: deep-link, refresh, resize to mobile,
tab-navigate with the keyboard, toggle themes, kill the backend and watch it
recover. Judged on whether it is genuinely pleasant, not merely functional.

**R10 — Learning & assessment.** Complete a learning path entirely in the UI.
Run a challenge, confirm the score matches the stated rubric.

**R11 — Team server.** Helm install on kind, two browsers as two users, confirm
isolation of runs and consistency of the leaderboard.

**R12 — Release verification.** Download the release artifacts, verify
checksums and the cosign signature, inspect the SBOM, run the binary on a
machine that has never seen the repo.
