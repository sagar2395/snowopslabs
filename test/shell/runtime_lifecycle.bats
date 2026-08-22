#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Runtime lifecycle idempotency (W3-T07, reliability slice). A reviewer will
# re-run `labctl init` and tear down repeatedly; cluster create must skip when
# the cluster exists, and delete must be a clean no-op when it is already gone.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command k3d kind kubectl docker
  ROOT="$(project_root)"
}

teardown() {
  stub_teardown
}

# --- k3d --------------------------------------------------------------------

@test "k3d up skips creation when the cluster already exists" {
  # The default k3d stub exits 0, so `cluster list <name>` reports it present.
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called k3d "cluster create"
  assert_called kubectl "use-context"
}

@test "k3d up creates the cluster when it is absent" {
  stub_when k3d "cluster list" 1 # not found
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called k3d "cluster create"
}

@test "k3d down is a clean no-op when the cluster is absent" {
  stub_when k3d "cluster list" 1
  run bash "$ROOT/runtimes/k3d/down.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called k3d "cluster delete"
}

# --- kind -------------------------------------------------------------------

@test "kind up skips creation when the cluster already exists" {
  stub_stdout kind "testcluster" # `get clusters` lists it
  run bash "$ROOT/runtimes/kind/up.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called kind "create cluster"
}

@test "kind down is a clean no-op when the cluster is absent" {
  # Default kind stub prints nothing, so `get clusters` is empty => not found.
  run bash "$ROOT/runtimes/kind/down.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called kind "delete cluster"
}
