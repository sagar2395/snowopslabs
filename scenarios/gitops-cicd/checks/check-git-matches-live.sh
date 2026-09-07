#!/usr/bin/env bash
set -euo pipefail

# The GitOps invariant, graded end to end: what the repo declares is what the
# cluster runs. Read the image and replica count out of Git itself — not out of
# the Application's status, which only reports what ArgoCD believes — and
# compare against the live Deployment.
#
# This fails for drift in either direction: someone edited the cluster and
# selfHeal is off, or someone pushed to Git and the sync never landed.

NS="gitops-demo"
DEPLOY="gitops-demo"

pod=$(kubectl -n gitops get pod -l app=git-server \
  -o jsonpath='{.items[?(@.status.phase=="Running")].metadata.name}' 2>/dev/null | awk '{print $1}')

if [ -z "${pod:-}" ]; then
  echo "FAIL: no running git-server pod in namespace gitops." >&2
  echo "      kubectl -n gitops get pods -l app=git-server" >&2
  exit 1
fi

manifest=$(kubectl -n gitops exec "$pod" -c git-daemon -- \
  git -C /srv/git/platform.git show HEAD:demo/deployment.yaml 2>/dev/null || echo "")

if [ -z "$manifest" ]; then
  echo "FAIL: could not read demo/deployment.yaml at HEAD of the lab repo." >&2
  echo "      Did a push remove or rename it? Check:" >&2
  echo "        kubectl -n gitops exec deploy/git-server -c git-daemon -- git -C /srv/git/platform.git ls-tree -r --name-only HEAD" >&2
  exit 1
fi

# Plain field reads: the seed manifest is flat and stays that way.
want_image=$(echo "$manifest" | awk '/^ *image:/ {print $2; exit}')
want_replicas=$(echo "$manifest" | awk '/^ *replicas:/ {print $2; exit}')

live_image=$(kubectl -n "$NS" get deployment "$DEPLOY" \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
live_replicas=$(kubectl -n "$NS" get deployment "$DEPLOY" \
  -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")

if [ -z "$live_image" ]; then
  echo "FAIL: Deployment ${DEPLOY} is not present in namespace ${NS}." >&2
  exit 1
fi

fail=0
if [ "$want_image" != "$live_image" ]; then
  echo "FAIL: Git declares image ${want_image}; the cluster runs ${live_image}." >&2
  fail=1
fi
if [ "$want_replicas" != "$live_replicas" ]; then
  echo "FAIL: Git declares ${want_replicas} replicas; the cluster runs ${live_replicas}." >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "      Git is the source of truth — do not fix this with kubectl." >&2
  echo "      Push the value you want, then let ArgoCD converge:" >&2
  echo "        kubectl -n argocd patch application gitops-demo --type merge \\" >&2
  echo "          -p '{\"operation\":{\"initiatedBy\":{\"username\":\"admin\"},\"sync\":{\"revision\":\"main\"}}}'" >&2
  exit 1
fi

echo "OK: Git and the cluster agree — image=${want_image}, replicas=${want_replicas}."
