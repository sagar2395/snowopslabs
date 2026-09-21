# Comparing application stacks

`labctl compare` runs one scenario against two or more workloads and reports the
difference. It answers the question the workload binding exists to make askable:
*what does this scenario look like if my application is the one under it?*

```bash
labctl compare run autoscaling-under-load --apps go-api,java-api
labctl compare list
labctl compare show 20260909-140000-autoscaling-under-load
```

The same runs are readable in the UI under **Analyze → Compare**.

## Why the flags exist

A comparison is only worth reading if the two runs were held to the same
conditions, so the fairness controls are the interesting part of the interface
(ADR-0014 §6).

| Flag | Default | What it is for |
|---|---|---|
| `--apps` | *(required)* | The workloads to measure, in order. **The first is the baseline** every other column is compared against. |
| `--profile` | `steady` | Traffic profile. `steady` keeps the offered rate flat, which is what a comparison wants — a spike shape would measure how each stack handles that shape instead. |
| `--rps` | `40` | Offered request rate, identical for every app. |
| `--warmup` | `1m` | Load time **excluded** from the measurement. |
| `--window` | `3m` | Length of the measured window. Minimum 2m. |
| `--keep` | off | Leave the scenario active after the last app, for inspecting the lab. |

Three rules are enforced rather than left to the operator:

- **One workload at a time.** Two apps under load on the same node contend for
  CPU, so each would be measured under the other's load and whichever ran
  second would look slower. Runs are strictly sequential.
- **The warmup is excluded.** Load runs for `--warmup` before the window opens,
  and the window is measured looking back over exactly its own length. Without
  this the comparison reports class loading and JIT warm-up — which is how a
  cross-stack comparison ends up measuring the runtime rather than the
  application.
- **Each app starts from the same state.** The scenario is torn down after every
  app, so the second is not measured on a cluster the first left scaled up.

A failure on any app fails the whole run. A comparison missing a column is not a
comparison, and printing one would be worse than stopping.

## What it measures

The set is fixed and derived from the app contract, so any conforming app —
including one you bring — is comparable without declaring anything extra. An
author who could add metrics per scenario could add one only their app
satisfies, and the comparison would stop being a comparison.

| Metric | Unit | Better |
|---|---|---|
| throughput | req/s | higher |
| latency p50 / p99 | ms | lower |
| tail amplification (p99 ÷ p50) | × | lower |
| 5xx rate | % | lower |
| peak ready replicas | — | neither |
| cpu (total, mean) | cores | lower |
| memory (total, peak) | MiB | lower |

CPU and memory are totals across replicas, not per pod: an app that answers the
same load on four replicas is not cheaper than one that answers it on two.

**These are measurements, not a grade.** The scenario's own checks remain the
only thing that passes or fails.

## Two traps this harness already fell into

Both were found running it, and both now fail loudly instead of quietly:

- **Starting the generator is not being under load.** The k6 job has to be
  scheduled, pull an image and ramp. The first real run opened its window on an
  idle app and reported `0.3 req/s` against 30 requested — a plausible-looking
  table built on no load at all. The harness now waits for load to arrive before
  the warmup clock starts, and refuses a window that saw less than half the
  offered rate.
- **A rate needs at least two scrapes.** With a 30s scrape interval, a `[30s]`
  range holds one sample and `rate()` returns *nothing* — which reads as "the
  app served zero", not as "ask again". That is why `--window` has a 2m floor.

## Requirements

Both apps must be deployable and expose the request metric named in their
`app.env` (`APP_REQUEST_METRIC`, defaulting to the OpenTelemetry semantic
convention name). A scenario is only comparable across apps that satisfy its
`prerequisites.capabilities` — `observability-sre` requires `otlp-tracing`, for
instance, which not every app claims.
