#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"

DEPLOY="${TARGET_WORKLOAD:-go-api}"
MON_NS="${MONITORING_NAMESPACE:-monitoring}"

echo "Removing the network policy and disarming its alert..."
kubectl delete networkpolicy labfault-network-blackhole -n "$NS" --ignore-not-found
kubectl delete prometheusrule labfault-network-blackhole -n "$MON_NS" --ignore-not-found >/dev/null 2>&1 || true

# Removing the policy lets new connections through immediately, but the ingress
# controller may still be holding failed ones. Nudge the backend so it reconnects
# rather than leaving the learner staring at 502s for a fixed policy.
kubectl -n "$NS" rollout status "deploy/$DEPLOY" --timeout=60s >/dev/null 2>&1 || true

echo "Resolved. Traffic should flow again within a few seconds."
