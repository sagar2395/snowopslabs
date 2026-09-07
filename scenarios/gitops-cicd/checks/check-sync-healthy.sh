#!/usr/bin/env bash
set -euo pipefail

# Grades the promise on the tin: ArgoCD has reconciled the Application and the
# workload it produced is healthy. This is what goes red when the Git source is
# unreachable, the path is wrong, or the manifests in Git do not apply.

APP="gitops-demo"

if ! kubectl -n argocd get application "$APP" >/dev/null 2>&1; then
  echo "FAIL: Application ${APP} does not exist in namespace argocd." >&2
  exit 1
fi

sync=$(kubectl -n argocd get application "$APP" -o jsonpath='{.status.sync.status}' 2>/dev/null || echo "")
health=$(kubectl -n argocd get application "$APP" -o jsonpath='{.status.health.status}' 2>/dev/null || echo "")

if [ "$sync" != "Synced" ]; then
  echo "FAIL: ${APP} sync status is '${sync:-Unknown}', not Synced." >&2
  echo "      What ArgoCD is complaining about:" >&2
  echo "        kubectl -n argocd get application ${APP} -o jsonpath='{.status.conditions}'" >&2
  exit 1
fi

if [ "$health" != "Healthy" ]; then
  echo "FAIL: ${APP} health status is '${health:-Unknown}', not Healthy." >&2
  echo "      Which resource is unhealthy:" >&2
  echo "        kubectl -n argocd get application ${APP} -o json | jq '.status.resources'" >&2
  exit 1
fi

echo "OK: ${APP} is Synced and Healthy."
