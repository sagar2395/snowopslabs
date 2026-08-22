#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="chaos-mesh"

echo "=== Chaos Mesh Status ==="
echo ""

# Check namespace
if ! kubectl get namespace $NAMESPACE >/dev/null 2>&1; then
  echo "Chaos Mesh is not installed (namespace $NAMESPACE not found)"
  exit 0
fi

# Pods
echo "Pods:"
kubectl get pods -n $NAMESPACE -o wide 2>/dev/null || echo "  No pods found"
echo ""

# Active experiments
echo "Active Chaos Experiments:"
for kind in PodChaos NetworkChaos StressChaos IOChaos TimeChaos DNSChaos HTTPChaos; do
  count=$(kubectl get "$kind" --all-namespaces --no-headers 2>/dev/null | wc -l)
  if [ "$count" -gt 0 ]; then
    echo "  $kind: $count"
    kubectl get "$kind" --all-namespaces --no-headers 2>/dev/null | while read -r line; do
      echo "    $line"
    done
  fi
done
echo ""

# Schedules
echo "Chaos Schedules:"
kubectl get schedules --all-namespaces --no-headers 2>/dev/null || echo "  No schedules found"
echo ""

# Workflows
echo "Chaos Workflows:"
kubectl get workflows --all-namespaces --no-headers 2>/dev/null || echo "  No workflows found"
echo ""

# Dashboard access
echo "Dashboard:"
echo "  kubectl port-forward -n $NAMESPACE svc/chaos-dashboard 2333:2333"
echo "  Then open http://localhost:2333"
