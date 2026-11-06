#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# runtimes/k3d/remove-agents.sh shrinks a k3d cluster by removing the agents
# add-agents.sh created, keeping init's agents and any node holding a volume.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command k3d kubectl docker
  ROOT="$(project_root)"
  WORK="$STUB_DIR/work"
  mkdir -p "$WORK/runtimes"
  cp -R "$ROOT/runtimes/k3d" "$ROOT/runtimes/_lib" "$WORK/runtimes/"
  export CLUSTER_NAME=lab
}

teardown() {
  stub_teardown
}

@test "removes an added agent the cluster no longer needs" {
  stub_when kubectl "get pv" 0
  stub_stdout kubectl "node/k3d-lab-agent-0
node/k3d-lab-agent-0-0"
  cd "$WORK"
  run bash runtimes/k3d/remove-agents.sh 1
  [ "$status" -eq 0 ]
  assert_called kubectl "drain k3d-lab-agent-0-0 --ignore-daemonsets"
  assert_called k3d "node delete k3d-lab-agent-0-0"
  assert_called kubectl "delete node k3d-lab-agent-0-0"
}

@test "takes a removed agent out of the load balancer" {
  # A rolled init agent (agent-0 became agent-0-0) looks like an added one and
  # is listed in the load balancer, which k3d updates for servers only.
  stub_when kubectl "get pv" 0 "k3d-lab-agent-1-0"
  stub_stdout kubectl "node/k3d-lab-agent-0-0
node/k3d-lab-agent-1-0"
  stub_script docker <<STUB
case "\$*" in
  "ps -a --filter label=k3d.cluster=lab "*) printf 'k3d-lab-server-0 server\nk3d-lab-agent-1-0 agent\n' ;;
  "cp k3d-lab-serverlb:/etc/confd/values.yaml "*)
    printf 'ports:\n  80.tcp:\n  - k3d-lab-server-0\n  - k3d-lab-agent-0-0\n' >"\$3" ;;
  "cp "*" k3d-lab-serverlb:/etc/confd/values.yaml") cp "\$2" "$STUB_DIR/lb-values" ;;
esac
exit 0
STUB
  cd "$WORK"
  run bash runtimes/k3d/remove-agents.sh 1
  [ "$status" -eq 0 ]
  assert_called k3d "node delete k3d-lab-agent-0-0"
  [ "$(cat "$STUB_DIR/lb-values")" = "$(printf 'ports:\n  80.tcp:\n  - k3d-lab-server-0\n  - k3d-lab-agent-1-0')" ]
  assert_called docker "restart k3d-lab-serverlb"
}

@test "never removes the agents init created" {
  stub_stdout kubectl "node/k3d-lab-agent-0
node/k3d-lab-agent-1"
  cd "$WORK"
  run bash runtimes/k3d/remove-agents.sh 1
  [ "$status" -eq 0 ]
  refute_called k3d "node delete"
}

@test "keeps an added agent that holds a local volume" {
  stub_when kubectl "get pv" 0 "k3d-lab-agent-0-0"
  stub_stdout kubectl "node/k3d-lab-agent-0
node/k3d-lab-agent-0-0"
  cd "$WORK"
  run bash runtimes/k3d/remove-agents.sh 1
  [ "$status" -eq 0 ]
  [[ "$output" == *"holds a local volume"* ]] || false
  refute_called kubectl "drain"
  refute_called k3d "node delete"
}

@test "keeps an added agent whose pods cannot be moved" {
  stub_when kubectl "get pv" 0
  stub_when kubectl "drain" 1
  stub_stdout kubectl "node/k3d-lab-agent-0
node/k3d-lab-agent-0-0"
  cd "$WORK"
  run bash runtimes/k3d/remove-agents.sh 1
  [ "$status" -eq 0 ]
  assert_called kubectl "uncordon k3d-lab-agent-0-0"
  refute_called k3d "node delete"
}

@test "does nothing when the cluster has no more agents than needed" {
  stub_when kubectl "get nodes" 0 "node/k3d-lab-agent-0"
  cd "$WORK"
  run bash runtimes/k3d/remove-agents.sh 1
  [ "$status" -eq 0 ]
  refute_called kubectl "drain"
}
