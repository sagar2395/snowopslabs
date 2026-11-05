#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# runtimes/k3d/add-agents.sh grows a running k3d cluster to a total number of
# agent nodes and makes the new nodes able to run the lab's app images.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command k3d kubectl docker
  ROOT="$(project_root)"
  WORK="$STUB_DIR/work"
  mkdir -p "$WORK/apps/go-api" "$WORK/runtimes"
  touch "$WORK/apps/go-api/app.env"
  cp -R "$ROOT/runtimes/k3d" "$ROOT/runtimes/_lib" "$WORK/runtimes/"
  export CLUSTER_NAME=lab
}

teardown() {
  stub_teardown
}

@test "adds the missing agents and imports the app images into them" {
  stub_when kubectl "get nodes" 0 "node/k3d-lab-agent-0"
  stub_when docker "images" 0 "go-api:v1.2.0"
  cd "$WORK"
  run bash runtimes/k3d/add-agents.sh 2
  [ "$status" -eq 0 ]
  assert_call_count k3d 1 "node create"
  assert_called k3d "node create lab-agent-1 --cluster lab --role agent"
  assert_called k3d "image import go-api:v1.2.0 --cluster lab"
  assert_called kubectl "wait --for=condition=Ready node k3d-lab-agent-1-0"
}

@test "imports every build of each app, not only the one running" {
  stub_when kubectl "get nodes" 0 "node/k3d-lab-agent-0"
  stub_script docker <<'STUB'
case "$*" in images*) printf 'go-api:latest\ngo-api:v1.2.0\ngo-api:<none>\n' ;; esac
exit 0
STUB
  cd "$WORK"
  run bash runtimes/k3d/add-agents.sh 2
  [ "$status" -eq 0 ]
  assert_called k3d "image import go-api:latest go-api:v1.2.0 --cluster lab"
  refute_called k3d "<none>"
}

@test "skips a name an earlier add already used" {
  stub_stdout kubectl "node/k3d-lab-agent-0
node/k3d-lab-agent-1-0"
  cd "$WORK"
  run bash runtimes/k3d/add-agents.sh 3
  [ "$status" -eq 0 ]
  assert_call_count k3d 1 "node create"
  assert_called k3d "node create lab-agent-2 --cluster lab"
}

@test "does nothing when the cluster already has enough agents" {
  stub_when kubectl "get nodes" 0 "node/k3d-lab-agent-0"
  cd "$WORK"
  run bash runtimes/k3d/add-agents.sh 1
  [ "$status" -eq 0 ]
  refute_called k3d "node create"
}

@test "gives each new agent one share of Docker's memory" {
  stub_when kubectl "get nodes" 0 "node/k3d-lab-agent-0"
  stub_when docker "MemTotal" 0 "8589934592" # 8 GiB
  cd "$WORK"
  run bash runtimes/k3d/add-agents.sh 2
  [ "$status" -eq 0 ]
  # A server and two agents: 2730 MiB each, the other 5462 MiB held back.
  assert_called k3d "node create lab-agent-1 --cluster lab --role agent --k3s-arg --kubelet-arg=system-reserved=memory=5462Mi --wait"
}

@test "adds agents as before when Docker's memory cannot be read" {
  stub_when kubectl "get nodes" 0 "node/k3d-lab-agent-0"
  cd "$WORK"
  run bash runtimes/k3d/add-agents.sh 2
  [ "$status" -eq 0 ]
  assert_called k3d "node create lab-agent-1 --cluster lab --role agent --wait"
}

@test "fails when k3d cannot create the node" {
  stub_when kubectl "get nodes" 0 "node/k3d-lab-agent-0"
  stub_when k3d "node create" 1
  cd "$WORK"
  run bash runtimes/k3d/add-agents.sh 2
  [ "$status" -ne 0 ]
}
