#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Tests for the stub harness itself. If these fail, every other shell test in
# the repo is untrustworthy — so they run first and assert the harness's own
# contract: recording, failure injection, per-argument rules, and the
# idempotency helpers that W3's provider tests depend on.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command kubectl helm
}

teardown() {
  stub_teardown
}

@test "stubs shadow the real binary on PATH" {
  run command -v kubectl
  [ "$status" -eq 0 ]
  [[ "$output" == "$STUB_BIN/kubectl" ]]
}

@test "calls are recorded with their arguments" {
  kubectl get pods -n monitoring
  assert_called kubectl "get pods"
  assert_called kubectl "-n monitoring"
  assert_call_count kubectl 1
}

@test "an unconfigured stub succeeds silently" {
  run helm upgrade --install foo ./chart
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "stub_exit makes a tool fail so failure propagation can be tested" {
  stub_exit helm 1
  run helm upgrade --install foo ./chart
  [ "$status" -eq 1 ]
}

@test "a failing tool aborts a set -euo pipefail script" {
  stub_exit helm 1
  script="$STUB_DIR/script.sh"
  cat >"$script" <<'EOS'
#!/usr/bin/env bash
set -euo pipefail
helm upgrade --install foo ./chart
kubectl apply -f after.yaml
EOS
  chmod +x "$script"
  run bash "$script"
  [ "$status" -ne 0 ]
  # The script must not have continued past the failure.
  refute_called kubectl "apply"
}

@test "stub_stdout feeds output back to the script" {
  stub_stdout kubectl "nginx-abc123"
  run kubectl get pods -o name
  [ "$status" -eq 0 ]
  [[ "$output" == "nginx-abc123" ]]
}

@test "stub_when matches on arguments, first rule wins" {
  stub_when helm "list" 0 "my-release"
  stub_when helm "upgrade" 1 ""

  run helm list -n default
  [ "$status" -eq 0 ]
  [[ "$output" == "my-release" ]]

  run helm upgrade --install my-release ./chart
  [ "$status" -eq 1 ]
}

@test "stub_when falls through to the default when nothing matches" {
  stub_when helm "rollback" 1 ""
  stub_stdout helm "default output"
  run helm status my-release
  [ "$status" -eq 0 ]
  [[ "$output" == "default output" ]]
}

@test "refute_called fails when the command was called" {
  kubectl delete ns monitoring
  run refute_called kubectl "delete"
  [ "$status" -ne 0 ]
}

@test "assert_called fails with the recorded calls in its message" {
  kubectl get pods
  run assert_called kubectl "apply"
  [ "$status" -ne 0 ]
  [[ "$output" == *"recorded calls"* ]]
  [[ "$output" == *"get pods"* ]]
}

@test "reset_calls clears history for idempotency assertions" {
  kubectl apply -f a.yaml
  assert_call_count kubectl 1
  reset_calls
  assert_call_count kubectl 0
  refute_called kubectl
}

@test "call_count counts only matching invocations" {
  kubectl apply -f a.yaml
  kubectl apply -f b.yaml
  kubectl get pods
  [ "$(call_count kubectl)" -eq 3 ]
  [ "$(call_count kubectl "apply")" -eq 2 ]
  [ "$(call_count kubectl "delete")" -eq 0 ]
}

@test "project_root resolves the repository root" {
  root="$(project_root)"
  [ -f "$root/go.mod" ]
  [ -d "$root/platform" ]
}
