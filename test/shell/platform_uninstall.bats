#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Reliability guard for platform teardown (W3, reliability-first slice).
#
# Every provider's uninstall.sh must (1) be a safe idempotent no-op under
# stubs — running it when nothing is installed, or running it twice, must not
# error — and (2) never issue an *unbounded* `kubectl delete namespace`, which
# blocks forever on a namespace stuck Terminating and hangs `labctl lab down`.
# These run over ALL providers so a new one cannot regress the guarantee.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command kubectl helm
  ROOT="$(project_root)"
}

teardown() {
  stub_teardown
}

# uninstall_scripts prints every provider uninstall.sh (2- and 3-level layouts).
uninstall_scripts() {
  ls "$ROOT"/platform/*/*/uninstall.sh "$ROOT"/platform/*/*/*/uninstall.sh 2>/dev/null
}

@test "there is at least one provider uninstall script to check" {
  run uninstall_scripts
  [ "$status" -eq 0 ]
  [ -n "$output" ]
}

@test "every uninstall.sh exits 0 under stubs (idempotent no-op)" {
  local bad=""
  while IFS= read -r script; do
    [ -f "$script" ] || continue
    reset_calls
    run bash "$script"
    [ "$status" -eq 0 ] || bad="$bad
$script exited $status: $output"
  done < <(uninstall_scripts)
  [ -z "$bad" ] || { printf 'non-idempotent uninstall(s):%s\n' "$bad"; return 1; }
}

@test "every uninstall.sh is safe to run twice (converges)" {
  local bad=""
  while IFS= read -r script; do
    [ -f "$script" ] || continue
    run bash "$script"
    [ "$status" -eq 0 ] || bad="$bad
first run $script exited $status"
    run bash "$script"
    [ "$status" -eq 0 ] || bad="$bad
second run $script exited $status"
  done < <(uninstall_scripts)
  [ -z "$bad" ] || { printf 'uninstall not re-runnable:%s\n' "$bad"; return 1; }
}

@test "no uninstall issues an unbounded 'kubectl delete namespace' (never hangs)" {
  local bad=""
  while IFS= read -r script; do
    [ -f "$script" ] || continue
    reset_calls
    run bash "$script"
    while IFS= read -r line; do
      case "$line" in
        *"delete namespace"* | *"delete ns "*)
          case "$line" in
            *"--timeout"* | *"--wait=false"*) : ;;
            *) bad="$bad
$script: unbounded namespace delete: kubectl $line" ;;
          esac
          ;;
      esac
    done < <(calls_for kubectl)
  done < <(uninstall_scripts)
  [ -z "$bad" ] || { printf 'teardown hang risk:%s\n' "$bad"; return 1; }
}
