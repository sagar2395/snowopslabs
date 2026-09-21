#!/usr/bin/env bash
# Shared access to the lab's in-cluster Git repository.
#
# Every check that grades GitOps has to read what Git actually declares, not
# what ArgoCD believes it declares — the gap between those two is the whole
# subject. One helper so no two checks can disagree about how to look.

GIT_NS="gitops"
REPO="/srv/git/platform.git"

git_pod() {
  kubectl -n "$GIT_NS" get pod -l app=git-server \
    -o jsonpath='{.items[?(@.status.phase=="Running")].metadata.name}' 2>/dev/null | awk '{print $1}'
}

# git_repo <git args...> — run git inside the server against the bare repo.
git_repo() {
  pod="$(git_pod)"
  [ -n "${pod:-}" ] || return 1
  kubectl -n "$GIT_NS" exec "$pod" -c git-daemon -- git -C "$REPO" "$@" 2>/dev/null
}

# The paths tracked at HEAD, one per line.
git_files() { git_repo ls-tree -r --name-only HEAD; }
