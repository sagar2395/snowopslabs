#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(dirname "$0")"

echo "Applying network policies..."

# Apply default-deny and namespace isolation policies
kubectl apply -f "$SCRIPT_DIR/default-deny.yaml"
kubectl apply -f "$SCRIPT_DIR/allow-dns.yaml"
kubectl apply -f "$SCRIPT_DIR/allow-monitoring.yaml"
kubectl apply -f "$SCRIPT_DIR/allow-ingress.yaml"

echo ""
echo "Network policies applied successfully"
echo ""
echo "Policies applied:"
echo "  - default-deny: Deny all ingress/egress by default in labeled namespaces"
echo "  - allow-dns: Allow DNS resolution (port 53) for all pods"
echo "  - allow-monitoring: Allow Prometheus scraping from monitoring namespace"
echo "  - allow-ingress: Allow traffic from ingress controller"
echo ""
echo "View policies: kubectl get networkpolicies -A"
echo ""
echo "To opt-in a namespace: kubectl label namespace <name> network-policy=enforced"
