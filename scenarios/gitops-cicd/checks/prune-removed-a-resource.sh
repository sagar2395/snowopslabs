#!/usr/bin/env bash
set -euo pipefail

# Objective: prove prune. A file removed from Git is removed from the cluster.
#
# Graded on both halves at once, which is what makes it unfakeable. Deleting the
# Service with kubectl while it is still in Git gets it recreated by selfHeal;
# removing it from Git without syncing leaves it running. Only doing both — the
# deletion committed, and ArgoCD allowed to converge — satisfies this.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=checks/_git.sh
. "${SCRIPT_DIR}/_git.sh"

NS="gitops-demo"
SVC="gitops-demo"

FILES="$(git_files || true)"
if [ -z "$FILES" ]; then
  echo "PENDING: could not read the lab repo at HEAD." >&2
  echo "  kubectl -n gitops get pods -l app=git-server" >&2
  exit 1
fi

IN_GIT=0
printf '%s\n' "$FILES" | grep -qx 'demo/service.yaml' && IN_GIT=1

IN_CLUSTER=0
kubectl -n "$NS" get service "$SVC" >/dev/null 2>&1 && IN_CLUSTER=1

if [ "$IN_GIT" = "1" ]; then
  echo "PENDING: demo/service.yaml is still tracked in Git, so nothing has been pruned yet." >&2
  echo "  In your clone: git rm demo/service.yaml && git commit -m 'drop the service' && git push origin main" >&2
  echo "  Then sync, and watch the Service leave the cluster on its own." >&2
  exit 1
fi

if [ "$IN_CLUSTER" = "1" ]; then
  echo "FAIL: demo/service.yaml is gone from Git, but Service ${NS}/${SVC} is still running." >&2
  echo "  The deletion has not been reconciled. Either the sync has not happened yet —" >&2
  echo "    kubectl -n argocd patch application gitops-demo --type merge \\" >&2
  echo "      -p '{\"operation\":{\"initiatedBy\":{\"username\":\"admin\"},\"sync\":{\"revision\":\"main\"}}}'" >&2
  echo "  — or prune is off, in which case ArgoCD leaves orphans behind for ever:" >&2
  echo "    kubectl -n argocd get application gitops-demo -o jsonpath='{.spec.syncPolicy}'" >&2
  exit 1
fi

echo "OK: demo/service.yaml is absent from Git and Service ${NS}/${SVC} is gone from the cluster."
echo "A deletion in Git is a deletion in the cluster — that is prune."
