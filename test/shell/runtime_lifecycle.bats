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

@test "k3d up recreates the cluster when it exists but is unreachable" {
  # The cluster is listed (present) but its API never answers — the state a
  # half-built cluster leaves behind (broken load-balancer, stale kubeconfig).
  # `make init` must recover, not abort: delete the husk and recreate it.
  stub_when kubectl "get --raw" 1 # /healthz probe fails
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called k3d "cluster delete"
  assert_called k3d "cluster create"
}

@test "k3d up skips creation when an existing cluster is healthy" {
  # Present and reachable: no destructive delete, no recreate.
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called k3d "cluster delete"
  refute_called k3d "cluster create"
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

@test "kind up recreates the cluster when it exists but is unreachable" {
  stub_stdout kind "testcluster" # `get clusters` lists it
  stub_when kubectl "get --raw" 1 # /healthz probe fails
  run bash "$ROOT/runtimes/kind/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called kind "delete cluster"
  assert_called kind "create cluster"
}

@test "kind down is a clean no-op when the cluster is absent" {
  # Default kind stub prints nothing, so `get clusters` is empty => not found.
  run bash "$ROOT/runtimes/kind/down.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called kind "delete cluster"
}
