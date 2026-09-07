#!/usr/bin/env bash
set -euo pipefail

# The drill step: did YOU change the declared state in Git, and did ArgoCD then
# reconcile that commit into the cluster?
#
# Two conditions, because either alone is cheatable. More than the seed commit
# proves a push happened; the Application's synced revision matching Git HEAD
# proves the push is what the cluster is running.

pod=$(kubectl -n gitops get pod -l app=git-server \
  -o jsonpath='{.items[?(@.status.phase=="Running")].metadata.name}' 2>/dev/null | awk '{print $1}')

if [ -z "${pod:-}" ]; then
  echo "PENDING: no running git-server pod in namespace gitops." >&2
  exit 1
fi

commits=$(kubectl -n gitops exec "$pod" -c git-daemon -- \
  git -C /srv/git/platform.git rev-list --count HEAD 2>/dev/null || echo "0")
head=$(kubectl -n gitops exec "$pod" -c git-daemon -- \
  git -C /srv/git/platform.git rev-parse HEAD 2>/dev/null || echo "")

if [ "${commits:-0}" -lt 2 ]; then
  echo "PENDING: the lab repo still holds only the seed commit — nothing has been pushed yet." >&2
  exit 1
fi

synced=$(kubectl -n argocd get application gitops-demo \
  -o jsonpath='{.status.sync.revision}' 2>/dev/null || echo "")

if [ "$synced" != "$head" ]; then
  echo "PENDING: you pushed ${head}, but ArgoCD has only synced ${synced:-nothing}." >&2
  echo "         Automated sync polls every ~3 minutes. To stop waiting:" >&2
  echo "           kubectl -n argocd patch application gitops-demo --type merge \\" >&2
  echo "             -p '{\"operation\":{\"initiatedBy\":{\"username\":\"admin\"},\"sync\":{\"revision\":\"main\"}}}'" >&2
  exit 1
fi

echo "OK: ${commits} commits in the repo; ArgoCD has reconciled HEAD (${head})."
