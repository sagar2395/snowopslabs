#!/usr/bin/env bash
set -euo pipefail

# The drill step: did YOU change the declared state in Git, and is the cluster
# running your change?
#
# It deliberately does NOT compare revisions. ArgoCD's .status.sync.revision is
# the repo revision it last compared at, which moves to HEAD whenever any sync
# runs — including one triggered by a path this Application does not watch. So
# it lags behind HEAD after a commit to another path, and jumps ahead of the
# last commit that touched demo/ after the next sync. Neither comparison is
# right, and both produce a check that is red for reasons the learner did not
# cause.
#
# What is exact: compare the declared state at HEAD with the declared state in
# the seed commit. If they differ, someone pushed a real change. That the
# cluster is actually running it is the job of git-matches-live, which is not a
# pending step and is enforced on every run.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=checks/_git.sh
. "${SCRIPT_DIR}/_git.sh"

if [ -z "$(git_pod)" ]; then
  echo "PENDING: no running git-server pod in namespace gitops." >&2
  exit 1
fi

commits=$(git_repo rev-list --count HEAD || echo "0")
if [ "${commits:-0}" -lt 2 ]; then
  echo "PENDING: the lab repo still holds only the seed commit — nothing has been pushed yet." >&2
  echo "  Clone it, change demo/deployment.yaml, commit and push. The commands are in" >&2
  echo "  'labctl scenario info gitops-cicd'." >&2
  exit 1
fi

# The root commit is the seed. Reading it back beats hardcoding the seed values
# here, where they would quietly rot the day the seed ConfigMap changes.
seed_rev=$(git_repo rev-list --max-parents=0 HEAD | head -1 || echo "")
[ -n "$seed_rev" ] || {
  echo "PENDING: could not identify the seed commit." >&2
  exit 1
}

declared_now=$(git_repo show "HEAD:demo/deployment.yaml" |
  awk '/^ *image:/ {i=$2} /^ *replicas:/ {r=$2} END {print i, r}' || true)
declared_seed=$(git_repo show "${seed_rev}:demo/deployment.yaml" |
  awk '/^ *image:/ {i=$2} /^ *replicas:/ {r=$2} END {print i, r}' || true)

if [ -z "$declared_now" ]; then
  echo "PENDING: demo/deployment.yaml is missing at HEAD — the Application has nothing to deploy." >&2
  exit 1
fi

if [ "$declared_now" = "$declared_seed" ]; then
  echo "PENDING: there are ${commits} commits, but demo/deployment.yaml still declares the seed values" >&2
  echo "         (${declared_seed})." >&2
  echo "  Change what the cluster is asked to run — the image tag, the replica count — then push:" >&2
  echo "    sed -i.bak 's|nginx:1.27.3-alpine|nginx:1.27.4-alpine|; s|replicas: 2|replicas: 3|' demo/deployment.yaml" >&2
  echo "    git commit -am 'promote nginx 1.27.4, scale to 3' && git push origin main" >&2
  exit 1
fi

echo "OK: ${commits} commits in the repo; demo/deployment.yaml now declares ${declared_now}"
echo "(the seed declared ${declared_seed}). git-matches-live proves the cluster is running it."
