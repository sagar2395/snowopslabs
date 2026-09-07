#!/usr/bin/env bash
set -euo pipefail

# Binds the Application to the in-cluster Git repo, once the repo is actually
# serving. Applying it earlier just makes ArgoCD retry a refused connection and
# report ComparisonError to the learner on their first look.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="${SCRIPT_DIR}/../manifests/argocd-applications.yaml"

echo "Waiting for the git server to accept connections..."
kubectl -n gitops rollout status deployment/git-server --timeout=180s

echo "Binding the gitops-demo Application to git://git-server.gitops:9418/platform.git"
kubectl apply -f "$MANIFEST"

# Wait for the first reconcile to finish, health included. Sync goes green the
# moment the manifests are applied, so stopping there hands the learner a verify
# that fails on pods which are merely still pulling an image.
for _ in $(seq 1 36); do
  sync=$(kubectl -n argocd get application gitops-demo \
    -o jsonpath='{.status.sync.status}' 2>/dev/null || echo "")
  health=$(kubectl -n argocd get application gitops-demo \
    -o jsonpath='{.status.health.status}' 2>/dev/null || echo "")
  [ "$sync" = "Synced" ] && [ "$health" = "Healthy" ] && break
  sleep 5
done

echo "✓ Application gitops-demo bound (sync=${sync:-unknown}, health=${health:-unknown})"
