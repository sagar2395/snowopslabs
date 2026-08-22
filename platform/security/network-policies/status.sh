#!/usr/bin/env bash
set -euo pipefail

echo "Network Policy Status:"
echo "======================"
echo ""

echo "Network policies across all namespaces:"
kubectl get networkpolicies -A 2>/dev/null || echo "  No network policies found"

echo ""
echo "Namespaces with enforcement label:"
kubectl get namespaces -l network-policy=enforced --no-headers 2>/dev/null || echo "  No namespaces opted in"
