# SnowOps Labs — Product Definition (v2)

> What we are building and why. The *how* is `docs/architecture/ARCHITECTURE.md`.
> The *when* is `docs/ROADMAP.md`.

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

Production is the only place these skills are exercised, and production is the
worst possible classroom. Existing options each miss:

| Option | Gap |
|---|---|
| Katacoda-style browser labs | Toy clusters, no real observability stack, no failure realism |
| Certification courses (CKA/CKAD) | Test *knowledge*, not incident response under time pressure |
| Internal game days | Enormous setup cost; run twice a year at best |
| Just reading docs | No feedback loop, no way to know if you'd actually cope |

## What SnowOps Labs does

A single Go binary (`labctl`) plus a web UI drives four loops:

**1. Build a realistic platform.** Spin up a local cluster and install a real
stack — ingress, metrics, logs, traces, GitOps, service mesh, secrets,
autoscaling — from swappable providers. Not a toy: the same Helm charts you'd
run in production, at laptop scale.

**2. Run a scenario.** Declarative YAML stages a situation (an event-driven
architecture, a canary rollout, a cost-optimisation exercise), states its
objectives, and carries machine-verifiable `checks`. `labctl scenario verify`
tells you objectively whether you achieved the goal.

**3. Break it.** The incident library injects realistic, reversible faults —
OOM kills, bad configs, network blackholes, broken selectors, noisy
neighbours — while traffic flows and alerts fire. Progressive hints cost you
points. Time-to-detect and time-to-resolve are measured.

**4. Measure.** Learning paths chain scenarios and incidents into a curriculum.
Challenge mode adds a timer and hides the hints. Results persist, and on a
shared deployment a team leaderboard turns a game day into something people
actually want to win.

## Who it is for

| User | Primary loop | Success looks like |
|---|---|---|
| **Individual engineer learning** | Learn → scenario → incident | Completes a path, can debug the class of failure unaided |
| **Platform team evaluating** | Build → swap provider → compare | Picks mesh A over mesh B with evidence, in a day not a month |
| **SRE running a game day** | Break → measure | Team's MTTR drops measurably across sessions |
| **Lead assessing skills** | Challenge → leaderboard | Objective, reproducible signal on incident-response ability |

## Design principles (non-negotiable)

1. **Laptop-first.** The golden path is `k3d` on a MacBook or a Linux box, with
   no cloud account, no credit card, no network dependency beyond image pulls.
   If it does not work flawlessly there, it does not ship.
2. **Real components, real failures.** We install actual Prometheus, actual
   Istio, actual Kafka. Simulated failures are injected into real systems, not
   mocked. The observability signal an engineer sees is the real signal.
3. **Declarative content, orchestrating code.** Scenarios, incidents, paths,
   challenges and checks are YAML plus shell scripts. Go orchestrates, records,
   grades and serves — it never encodes the content itself. Anyone who can write
   YAML can author for SnowOps Labs.
4. **Everything is verifiable.** Checks are the core primitive. A scenario
   without checks is a blog post. Checks power grading, CI validation, incident
   resolution detection and progress tracking alike.
5. **Every run is durable.** What happened, when, with what output, is recorded
   and survives a restart. You can cancel a run, resume watching it, and read it
   back tomorrow.
6. **Cross-platform.** macOS (Apple Silicon and Intel) and modern Linux, always.
   No GNU-only flags, no cgo, no hardcoded architectures.
7. **Fast reset.** Snapshot and restore lab state so an engineer can retry a
   scenario in seconds, not by rebuilding a cluster.

## Explicitly out of scope

- **Cloud runtimes (AKS/EKS/GKE).** Cut in v2. They were never verified against
  a real account and split focus. Local (`k3d`, `kind`) and in-cluster only.
- **A marketplace, entitlement tiers, or paid editions.** Cut in v2. Replaced by
  a small documented extension seam so third-party content stays possible.
- **Hosted multi-tenant SaaS.** The shared in-cluster server supports a team on
  one cluster. It is not a tenant-isolated platform.
- **Reimplementing tools we wrap.** k6, Chaos Mesh, Helm, kubectl stay themselves.
- **Offensive security labs.** Defensive misconfiguration drills only.

## What "production grade" means here

This is the bar every wave is measured against:

- A user can cancel any long-running operation and the cluster is left coherent.
- Two conflicting operations cannot race; the second is rejected with a clear
  message naming the run that holds the lock.
- No output is ever silently dropped. Logs are persisted and replayable.
- Every failure mode produces an actionable error naming the cause and the fix.
- The tool tells you what is wrong with your environment before you hit it
  (`labctl doctor`).
- Every feature has automated tests at every applicable layer, and a runbook a
  human can follow to prove it works on real hardware.
