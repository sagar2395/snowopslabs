#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# The in-cluster runtime (team mode) completes the runtime coverage alongside
# runtime_lifecycle.bats (k3d, kind). Its whole contract is negative: it must
# use the hosting cluster and must NEVER create or delete one — deleting the
# cluster the server runs in would be catastrophic. These tests pin that.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command kubectl helm k3d kind
  ROOT="$(project_root)"
}

teardown() {
  stub_teardown
}

@test "incluster up succeeds when the API server is reachable" {
  run bash "$ROOT/runtimes/incluster/up.sh"
  [ "$status" -eq 0 ]
  assert_called kubectl "cluster-info"
}

@test "incluster up fails clearly when the API server is unreachable" {
  stub_when kubectl "cluster-info" 1
  run bash "$ROOT/runtimes/incluster/up.sh"
  [ "$status" -eq 1 ]
  [[ "$output" == *"cannot reach"* ]]
}

@test "incluster up never creates a cluster (uses the hosting one)" {
  run bash "$ROOT/runtimes/incluster/up.sh"
  [ "$status" -eq 0 ]
  refute_called k3d
  refute_called kind
}

@test "incluster down is a no-op that never deletes the hosting cluster" {
  run bash "$ROOT/runtimes/incluster/down.sh"
  [ "$status" -eq 0 ]
  refute_called k3d
  refute_called kind
  refute_called kubectl "delete"
}
