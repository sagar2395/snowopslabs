#!/usr/bin/env bash
set -euo pipefail

# Objective: prove selfHeal. A change made with kubectl is reverted to Git.
#
# The obvious way to grade this does not work. Once ArgoCD has reverted you the
# cluster matches Git, which is indistinguishable from never having touched it,
# and ArgoCD itself keeps no durable record: a self-heal appends no entry to
# .status.history, and argocd_app_sync_total resets when the controller restarts.
# Catching it in flight does not work either — the revert lands in a couple of
# seconds, well inside a single verify run.
#
# What does survive is the Deployment's own event trail. A revert appears as a
# scale-down FROM a replica count Git has never declared TO the count Git
# declares now. A rolling update cannot produce that: its intermediate steps
# always move between counts that were declared. Once seen, the observation is
# recorded so the pass outlives the events' one-hour TTL.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=checks/_git.sh
. "${SCRIPT_DIR}/_git.sh"

NS="gitops-demo"
DEPLOY="gitops-demo"
WITNESS="selfheal-witness"

if kubectl -n "$GIT_NS" get configmap "$WITNESS" >/dev/null 2>&1; then
  seen="$(kubectl -n "$GIT_NS" get configmap "$WITNESS" -o jsonpath='{.data.detail}' 2>/dev/null || true)"
  echo "OK: ${seen}"
  echo "kubectl is not how you change anything ArgoCD manages — that is selfHeal."
  exit 0
fi

want="$(git_repo show HEAD:demo/deployment.yaml | awk '/^ *replicas:/ {print $2; exit}' || true)"
if [ -z "$want" ]; then
  echo "PENDING: could not read the declared replica count from the lab repo." >&2
  exit 1
fi

# Every replica count the repo has ever committed, so a rolling update's own
# steps are not mistaken for someone editing the cluster.
declared="$(git_repo log -p --all -- demo/deployment.yaml |
  awk '/^\+ *replicas:/ {print $NF}' | sort -u | tr '\n' ' ')"
declared=" ${declared} "

reverted_from=""
while read -r from to; do
  [ -n "${to:-}" ] || continue
  [ "$to" = "$want" ] || continue
  case "$declared" in *" ${from} "*) continue ;; esac
  reverted_from="$from"
  break
done <<EOT
$(kubectl -n "$NS" get events \
  -o jsonpath='{range .items[?(@.reason=="ScalingReplicaSet")]}{.message}{"\n"}{end}' 2>/dev/null |
  sed -n 's/.*Scaled down replica set [^ ]* from \([0-9]*\) to \([0-9]*\).*/\1 \2/p')
EOT

if [ -n "$reverted_from" ]; then
  detail="ArgoCD scaled ${NS}/${DEPLOY} back from ${reverted_from} to the ${want} replicas Git declares"
  kubectl -n "$GIT_NS" create configmap "$WITNESS" \
    --from-literal=observed-at="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --from-literal=detail="$detail" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1 || true
  echo "OK: ${detail}."
  echo "kubectl is not how you change anything ArgoCD manages — that is selfHeal."
  exit 0
fi

echo "PENDING: nothing has scaled ${NS}/${DEPLOY} away from the ${want} replicas Git declares." >&2
echo "  Change the cluster by hand and watch it come back:" >&2
echo "    kubectl -n ${NS} scale deployment/${DEPLOY} --replicas=7" >&2
echo "    kubectl -n ${NS} get deployment ${DEPLOY} -w      # ctrl-c once it returns to ${want}" >&2
echo "    labctl scenario verify gitops-cicd" >&2
echo "  You do not need to be quick: the revert is graded from the Deployment's" >&2
echo "  event trail, not from catching it live." >&2
exit 1
