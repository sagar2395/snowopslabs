#!/usr/bin/env bash
set -euo pipefail

NS="${TARGET_NAMESPACE:-labfault-batch}"
MON_NS="${MONITORING_NAMESPACE:-monitoring}"

echo "Removing the batch workload and disarming its alert..."

# --wait=false: the namespace takes ~40s to finalise and nothing below depends
# on it being gone, so do not make the learner watch it.
kubectl delete namespace "$NS" --ignore-not-found --wait=false
kubectl delete prometheusrule labfault-noisy-neighbor -n "$MON_NS" --ignore-not-found 2>/dev/null || true

echo "Resolved. Node CPU recovers as soon as the pods are gone."
