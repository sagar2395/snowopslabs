# SnowOps Labs: what it is and why

> What we are building and why. How it works is in
> [`architecture/ARCHITECTURE.md`](architecture/ARCHITECTURE.md), and what
> comes next is in [`ROADMAP.md`](ROADMAP.md).

---

## One sentence

**SnowOps Labs is a Kubernetes platform-engineering simulator: it stands up a
realistic production-shaped cluster on your laptop in minutes, breaks it in
realistic ways, and grades you on how you fix it.**

## The problem

Platform and SRE engineers are expected to be good at things they almost never
get to practise:

- Diagnosing a CrashLoopBackOff at 3am with a pager going off.
- Deciding whether Istio or Linkerd fits, without a month-long spike.
- Draining a node under load without breaking the SLO.
- Knowing what "p99 latency regressed" actually looks like on a dashboard.

Production is the only place these skills get exercised, and production is the
worst possible classroom. The existing options each fall short:

| Option | Gap |
|---|---|
| Browser-based labs | Toy clusters, no real observability stack, no realistic failures |
| Certification courses (CKA/CKAD) | Test *knowledge*, not incident response under time pressure |
| Internal game days | Huge setup cost, so they happen twice a year at best |
| Reading docs | No feedback loop, no way to know whether you'd actually cope |

## What SnowOps Labs does

A single Go binary (`labctl`) with a built-in web UI drives four loops:

**1. Build a realistic platform.** Spin up a local cluster and install a real
stack (ingress, metrics, logs, traces, GitOps, service mesh, secrets,
autoscaling) from swappable providers. Not a toy: the same Helm charts you'd
run in production, at laptop scale.

**2. Run a scenario.** Declarative YAML sets up a situation (an event-driven
architecture, a canary rollout, a cost-optimisation exercise), states its
objectives, and carries machine-verifiable `checks`. `labctl scenario verify`
tells you objectively whether you achieved the goal.

**3. Break it.** The incident library injects realistic, reversible faults
(OOM kills, bad configs, network blackholes, broken selectors, noisy
neighbours) while traffic flows and alerts fire. Hints are there when you're
stuck, and time to detect and time to resolve are measured.

**4. Measure.** `labctl scenario verify` and `labctl incident status` grade
the outcome against the live cluster: checks passed, time to resolve, hints
used. Learning paths chain scenarios and incidents into a curriculum.
Challenge mode adds a timer and costs you points for hints. Results persist,
and on a shared deployment a team leaderboard turns a game day into something
people actually want to win.

## Who it is for

| User | Primary loop | Success looks like |
|---|---|---|
| **Individual engineer learning** | Learn → scenario → incident | Completes a path and can debug that class of failure unaided |
| **Platform team evaluating** | Build → swap provider → compare | Picks one mesh over another with evidence, in a day instead of a month |
| **SRE running a game day** | Break → measure | The team's time to resolve drops across sessions |
| **Lead assessing skills** | Challenge → leaderboard | A fair, repeatable read on incident-response ability |

## Design principles

1. **Laptop-first.** The golden path is `k3d` on a MacBook, a Linux box or WSL2,
   with no cloud account, no credit card and no network dependency beyond image
   pulls. If it does not work well there, it does not ship.
2. **Real components, real failures.** We install actual Prometheus, Istio and
   Kafka. Failures are injected into real systems, not mocked, so the signal an
   engineer sees is the real signal.
3. **Declarative content, orchestrating code.** Scenarios, incidents, paths,
   challenges and checks are YAML plus shell scripts. Go orchestrates, records,
   grades and serves, but never contains the content itself. Anyone who can
   write YAML can author for SnowOps Labs.
4. **Everything is verifiable.** Checks are the core primitive. A scenario
   without checks is a blog post. Checks power grading, CI validation, incident
   resolution and progress tracking alike.
5. **Every run is durable.** What happened, when and with what output is
   recorded and survives a restart. You can cancel a run, watch it again, and
   read it back tomorrow.
6. **Cross-platform.** macOS (Apple Silicon and Intel), modern Linux and WSL2,
   always. No GNU-only flags, no cgo, no hardcoded architectures.
7. **Fast reset.** `labctl lab snapshot` and `labctl lab restore` record and
   bring back which components, apps and scenarios are active, so an engineer
   can retry a scenario without rebuilding the cluster by hand.

## The open-source lab comes first

Everything above ships as open source under Apache-2.0, and the open core
(engine, CLI, SDK and community content) stays that way. Anything commercial
lives outside this repository and never gates what the open-source lab can do
([GOVERNANCE.md](../GOVERNANCE.md)). Third-party content uses the documented
extension seam ([ADR-0008](adr/0008-content-extensibility-seam.md)), not a
marketplace.

## Hosted labs: a long-term goal

Running the lab needs Docker with a few GB of memory to spare, and some people
won't have that, or would rather not run Kubernetes on their own machine.
A hosted version, where the same scenarios run on a cluster we operate and you
open them in a browser, is something we want to build eventually.

It is not on the near-term roadmap. It comes after the open-source lab has a
solid base of users, and what it looks like will depend on what those users
ask for. When it happens:

- It is a separate service built on the open-source lab, not a paid edition of
  it, and it never takes features away from the local lab.
- Any change it needs in this repository goes through an RFC first.

See [ADR-0016](adr/0016-hosted-labs-long-term.md) for the reasoning.

## Not in scope today

- **Cloud runtimes for your own cluster (AKS, EKS, GKE).** Local (`k3d`, `kind`)
  and `incluster` only. The runtime profile contract would allow a cloud
  profile later, verified against a real account ([ADR-0001](adr/0001-cut-cloud-and-commercial-scope.md)).
- **A content marketplace or paid editions.** See above.
- **A multi-tenant platform in this repository.** The shared in-cluster server
  supports one team on one cluster; it does not isolate tenants from each other.
- **Reimplementing the tools we wrap.** k6, Chaos Mesh, Helm and kubectl stay
  themselves.
- **Offensive security labs.** Defensive misconfiguration drills only.

## What "production grade" means here

This is the bar every change is measured against:

- A user can cancel any long-running operation and the cluster is left coherent.
- Two conflicting operations cannot race; the second is rejected with a clear
  message naming the run that holds the lock.
- No output is ever silently dropped. Logs are persisted and replayable.
- Every failure produces an actionable error naming the cause and the fix.
- The tool tells you what is wrong with your environment before you hit it
  (`labctl doctor`).
- Every feature has automated tests at every layer it touches, and a runbook a
  human can follow to prove it works on real hardware.
