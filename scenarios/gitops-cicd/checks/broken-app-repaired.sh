#!/usr/bin/env bash
set -euo pipefail

# Objective: diagnose a failed release and repair it IN GIT.
#
# The lab seeds the 'broken' path with a Deployment whose image tag does not
# exist. It syncs cleanly — the manifests are valid YAML — and then goes
# Degraded, which is what a real bad deploy looks like: nothing is wrong with
# your pipeline, the release itself is wrong.
#
# Graded on health rather than on the image string, so any tag that actually
# pulls counts. selfHeal makes `kubectl set image` a dead end on its own, which
# is the point: the repair has to be committed.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=checks/_git.sh
. "${SCRIPT_DIR}/_git.sh"

APP="gitops-broken"

if ! kubectl -n argocd get application "$APP" >/dev/null 2>&1; then
  echo "PENDING: Application ${APP} is not present." >&2
  echo "  Re-activate the scenario: labctl scenario up gitops-cicd --force" >&2
  exit 1
fi

sync=$(kubectl -n argocd get application "$APP" -o jsonpath='{.status.sync.status}' 2>/dev/null || echo "")
health=$(kubectl -n argocd get application "$APP" -o jsonpath='{.status.health.status}' 2>/dev/null || echo "")

if [ "$sync" = "Synced" ] && [ "$health" = "Healthy" ]; then
  image="$(git_repo show HEAD:broken/deployment.yaml | awk '/^ *image:/ {print $2; exit}' || true)"
  echo "OK: ${APP} is Synced and Healthy — Git now declares ${image:-a working image}."
  echo "You fixed a bad release the way GitOps requires: by changing what Git says."
  exit 0
fi

echo "PENDING: ${APP} is sync=${sync:-Unknown} health=${health:-Unknown}." >&2
echo "  This one ships broken on purpose. Diagnose it, then repair it in Git." >&2
echo "  What ArgoCD thinks:" >&2
echo "    kubectl -n argocd get application ${APP} -o jsonpath='{.status.conditions}'; echo" >&2
echo "  What the pods say — this is where the real reason is:" >&2
echo "    kubectl -n gitops-broken get pods" >&2
echo "    kubectl -n gitops-broken describe pod -l app=gitops-broken | tail -20" >&2
echo "  Fix it in your clone (broken/deployment.yaml), commit, push, then sync." >&2
echo "  Changing it with 'kubectl set image' is reverted by selfHeal within seconds." >&2
exit 1
