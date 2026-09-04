#!/usr/bin/env bash
# Passes when the observability-sre scenario has installed its own alerting
# rules. Prometheus itself is a platform prerequisite of that scenario, so
# probing Prometheus would be green before the learner activates anything.
set -euo pipefail

NS="${MONITORING_NAMESPACE:-monitoring}"
kubectl -n "$NS" get prometheusrule scenario-observability-sre-alerts >/dev/null 2>&1
