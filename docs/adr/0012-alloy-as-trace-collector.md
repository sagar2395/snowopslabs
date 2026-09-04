# ADR 0012 — Grafana Alloy as the trace collector; Promtail retained for logs

**Status:** Accepted
**Date:** 2026-09-04
**Wave:** W4

## Context

`go-api` supports OpenTelemetry tracing, but no values file ever set
`OTEL_EXPORTER_OTLP_ENDPOINT`, so the tracer provider was never initialised and
Grafana showed no traces. Fixing that raised a design question: what should the
application export to?

Pointing the application straight at Tempo is the smallest change and the wrong
lesson. Production systems put a collector between the two so that batching,
retry, sampling and backend selection are operational concerns rather than
application code.

Separately, Promtail — which ships the lab's logs — is deprecated and
end-of-life, with Grafana Alloy as its successor (ADR-0011).

## Decision

Grafana Alloy is introduced as an **OTLP trace collector**, not yet as a log
shipper.

- `observability-sre` installs `grafana/alloy` 1.12.1 as a single-replica
  Deployment (a trace gateway receives over the network; it does not need to be
  a per-node DaemonSet).
- Its config is three blocks a learner can read end to end: an OTLP receiver
  (gRPC 4317, HTTP 4318), a batch processor, and an OTLP exporter to Tempo.
- The app is wired to it by `scripts/enable-tracing.sh`, a plain
  `kubectl set env` on the Deployment, reversed by `scripts/disable-tracing.sh`
  on `scenario down` (which is what the new `uninstallScript` component field
  is for).
- Promtail continues to ship logs, pinned, until a later change moves log
  collection to the Alloy instance that is now already running.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| App exports directly to Tempo | Smallest change, but teaches an anti-pattern and couples every app to the backend's address and protocol. |
| Migrate logs to Alloy at the same time | Changes the log pipeline's label set (`app`, `namespace`) while log delivery was itself being fixed. Two variables, one debugging session. |
| OpenTelemetry Collector instead of Alloy | Equivalent for this purpose. Alloy is the successor to the Promtail already in the stack, so one component eventually covers logs, metrics and traces. |
| Alloy as a DaemonSet | Wasteful for a trace gateway, and misleading — it implies node-local collection, which is a log-shipping concern. |

## Consequences

- Swapping trace backends is a change to one exporter block, with no application
  redeploy — demonstrable in the lab.
- Alloy is already deployed when the log migration happens: that change becomes
  a config addition rather than a new component.
- Two agents run in the monitoring namespace during the overlap (Promtail
  DaemonSet, Alloy Deployment), costing roughly 128Mi. Accepted.
- Tracing is off in the app's chart by default (`tracing.enabled: false`). With
  no collector listening, the OTLP exporter retries every batch and floods the
  log — the scenario turns it on once Alloy is running.
