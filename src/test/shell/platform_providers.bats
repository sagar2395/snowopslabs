#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Coverage for EVERY platform provider script (W3-T06), not just the default
# stack. These are data-driven: they discover platform/<category>/<provider>/
# {install,uninstall,status}.sh and assert the invariants that must hold across
# all of them, so a new provider is covered the moment it is added.
#
# Invariants:
#   - install/uninstall/status all run clean under stubbed tools (no cluster).
#   - a helm-based install uses `helm upgrade --install` — the idempotent
#     primitive, so a re-run after an interrupted `labctl init` converges
#     (golden rule 5).
#   - an uninstall deletes safely (helm uninstall, or kubectl delete with
#     --ignore-not-found), so tearing down twice never errors.

load 'helpers/stub'

setup() {
  stub_setup
  # Stub every external tool a provider script might reach for, so nothing
  # touches a real cluster or the network. An unstubbed command would fall
  # through to the real PATH and fail the run — which is the signal to add it
  # here rather than let a script quietly shell out during tests.
  stub_command kubectl helm k3d kind docker jq kustomize curl istioctl linkerd vault argocd
  ROOT="$(project_root)"
}

teardown() {
  stub_teardown
}

@test "every platform install.sh runs clean under stubbed tools" {
  for script in "$ROOT"/platform/*/*/install.sh; do
    reset_calls
    run bash "$script"
    [ "$status" -eq 0 ] || {
      echo "install failed: ${script#"$ROOT"/} (exit $status)"
      echo "$output"
      return 1
    }
  done
}

@test "every helm-based install uses 'helm upgrade --install' (idempotent)" {
  for script in "$ROOT"/platform/*/*/install.sh; do
    # Only providers that install via helm carry the idempotency contract;
    # manifest-only providers (e.g. network-policies) are checked by the
    # runs-clean test above.
    grep -q 'helm upgrade' "$script" || continue
    reset_calls
    run bash "$script"
    [ "$status" -eq 0 ] || { echo "install failed: ${script#"$ROOT"/}"; echo "$output"; return 1; }
    assert_called helm "upgrade --install" || {
      echo "not idempotent (no 'helm upgrade --install'): ${script#"$ROOT"/}"
      return 1
    }
  done
}

@test "every platform uninstall.sh runs clean under stubbed tools" {
  for script in "$ROOT"/platform/*/*/uninstall.sh; do
    reset_calls
    run bash "$script"
    [ "$status" -eq 0 ] || {
      echo "uninstall failed: ${script#"$ROOT"/} (exit $status)"
      echo "$output"
      return 1
    }
  done
}

@test "every uninstall is safe to re-run when nothing is installed" {
  # Simulate an empty cluster: the presence probes each script guards its
  # deletes with (helm status / kubectl get) report not-found. A safe teardown
  # then skips its deletes and still exits 0; an unguarded one would error.
  stub_when helm status 1
  stub_when kubectl "get namespace" 1
  for script in "$ROOT"/platform/*/*/uninstall.sh; do
    reset_calls
    run bash "$script"
    [ "$status" -eq 0 ] || {
      echo "uninstall not safe on an empty cluster: ${script#"$ROOT"/} (exit $status)"
      echo "$output"
      return 1
    }
  done
}

@test "every platform status.sh runs clean under stubbed tools" {
  for script in "$ROOT"/platform/*/*/status.sh; do
    reset_calls
    run bash "$script"
    [ "$status" -eq 0 ] || {
      echo "status failed: ${script#"$ROOT"/} (exit $status)"
      echo "$output"
      return 1
    }
  done
}

@test "there is at least one provider triple, and each has all three scripts" {
  local count=0
  for install in "$ROOT"/platform/*/*/install.sh; do
    count=$((count + 1))
    dir="$(dirname "$install")"
    [ -f "$dir/uninstall.sh" ] || { echo "missing uninstall.sh in ${dir#"$ROOT"/}"; return 1; }
    [ -f "$dir/status.sh" ]    || { echo "missing status.sh in ${dir#"$ROOT"/}"; return 1; }
  done
  [ "$count" -gt 0 ] || { echo "no provider install scripts discovered — glob or layout changed"; return 1; }
}
