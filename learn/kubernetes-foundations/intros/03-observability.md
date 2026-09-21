# Module 3 — Enable observability

## What you'll do

Activate the `observability-sre` scenario, which installs Prometheus,
Grafana, and pre-built SRE dashboards. You'll use these in the next module
to watch the effects of an injected fault.

## Background

Scenarios are declarative: `scenario.yaml` lists Helm charts, manifests,
and dashboards to install. The scenario engine tracks what's active so you
can deactivate and re-activate safely.

## Objective

Activate the scenario and verify Prometheus is reachable.

```bash
bin/labctl scenario up observability-sre
# Wait for pods to become ready (~2 minutes on first install)
bin/labctl scenario verify observability-sre --watch --timeout 5m
```

**Completion check:** the scenario's own alerting rules are installed
(`kubectl -n monitoring get prometheusrule scenario-observability-sre-alerts`).

Note what is *not* checked: whether Prometheus answers. Prometheus is part of
the platform and is already running before you start this module, so probing it
would pass before you did anything. A module has to be graded on what its work
produced.
