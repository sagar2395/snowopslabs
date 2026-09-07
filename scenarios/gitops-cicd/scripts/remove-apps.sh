#!/usr/bin/env bash
set -euo pipefail

# Teardown, in the one order that does not wedge.
#
# ArgoCD's resources-finalizer cascades a delete through everything an
# Application manages. If the controller is uninstalled before that finalizer
# runs, the delete never completes and the namespace hangs in Terminating
# forever. So: delete the Application first, while the controller is still
# alive, then remove what it managed explicitly.

if kubectl -n argocd get application gitops-demo >/dev/null 2>&1; then
  echo "Removing the gitops-demo Application..."
  kubectl -n argocd patch application gitops-demo --type merge \
    -p '{"metadata":{"finalizers":[]}}' >/dev/null 2>&1 || true
  kubectl -n argocd delete application gitops-demo --ignore-not-found --timeout=60s || true
fi

echo "Removing the workload the Application managed..."
kubectl delete namespace gitops-demo --ignore-not-found --timeout=120s || true

echo "✓ gitops-demo removed"
