#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# cluster-upgrade-drill's roll-node.sh replaces a drained k3d agent with one on
# the target version and repoints the cluster's load balancer at it. The load
# balancer also fronts the API server, so the script hands back only once the
# API answers again; otherwise the learner's next kubectl is refused.

load 'helpers/stub'

setup() {
  stub_setup
  ROOT="$(project_root)"
  SCRIPT="$ROOT/scenarios/cluster-upgrade-drill/scripts/roll-node.sh"
  export CLUSTER_NAME=lab TARGET_K3S_VERSION=v1.33.6-k3s1 STATE="$BATS_TEST_TMPDIR/state"
  mkdir -p "$STATE"
  echo 0 >"$STATE/readyz-failures"

  stub_script sleep <<'STUB'
exit 0
STUB
  stub_script k3d <<'STUB'
case "$*" in "node create"*) touch "$STATE/created" ;; esac
exit 0
STUB
  # The load balancer's upstream list names the node being rolled.
  stub_script docker <<'STUB'
case "$*" in
  "inspect k3d-lab-serverlb") exit 0 ;;
  "ps -a --filter label=k3d.cluster=lab "*) printf 'k3d-lab-server-0 server\nk3d-lab-agent-0-0 agent\n' ;;
  "cp k3d-lab-serverlb:/etc/confd/values.yaml "*) printf 'ports:\n  80.tcp:\n  - k3d-lab-server-0\n  - k3d-lab-agent-0\n' >"$3" ;;
  "cp "*" k3d-lab-serverlb:/etc/confd/values.yaml") cp "$2" "$STATE/lb-values" ;;
  "restart k3d-lab-serverlb") touch "$STATE/lb-restarted" ;;
  # The lab's go-api builds: the tag running now, and the one data-writer uses.
  "images --format {{.Repository}}:{{.Tag}} go-api") printf 'go-api:latest\ngo-api:v1.2.0\n' ;;
esac
exit 0
STUB
  stub_script kubectl <<'STUB'
case "$*" in
  *"node-role.kubernetes.io/control-plane"*) echo "v1.33.6+k3s1" ;;
  "get node k3d-lab-agent-0 -o jsonpath={.status.nodeInfo.kubeletVersion}") echo "${KUBELET_VERSION:-v1.33.6+k3s1}" ;;
  "get node k3d-lab-agent-0 -o jsonpath={.spec.unschedulable}") echo true ;;
  "get node k3d-lab-agent-0") exit 0 ;;
  *"get pods --all-namespaces"*"{.image}"*) printf 'go-api:v1.2.0\nquay.io/prometheus/node-exporter:v1.9.0\n' ;;
  *"metadata.uid=='uid-new'"*) echo k3d-lab-agent-0-0 ;;
  *"jsonpath={range .items[*]}{.metadata.uid}"*)
    printf 'uid-old\nuid-server\n'
    [ ! -f "$STATE/created" ] || echo uid-new
    ;;
  "get --raw /readyz")
    # Down while the load balancer restarts, for READYZ_FAILURES polls.
    n=$(cat "$STATE/readyz-failures")
    echo $((n + 1)) >"$STATE/readyz-failures"
    [ "$n" -ge "${READYZ_FAILURES:-0}" ]
    exit $?
    ;;
esac
exit 0
STUB
}

teardown() { stub_teardown; }

@test "after restarting the load balancer it waits until the API server answers" {
  READYZ_FAILURES=3 run bash "$SCRIPT" k3d-lab-agent-0
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ -f "$STATE/lb-restarted" ]
  [ "$(cat "$STATE/lb-values")" = "$(printf 'ports:\n  80.tcp:\n  - k3d-lab-server-0\n  - k3d-lab-agent-0-0')" ]
  [ "$(call_count kubectl "get --raw /readyz")" -eq 4 ]
  [[ "$output" == *"replaced by k3d-lab-agent-0-0"* ]] || false
  in_log=$(grep -n "restart k3d-lab-serverlb\|get --raw /readyz" "$STUB_LOG" | head -1)
  [[ "$in_log" == *"restart k3d-lab-serverlb"* ]] || false
}

# A later scenario may run a build no pod uses at roll time: backup-restore's
# data-writer runs go-api:latest with imagePullPolicy Never, so without it the
# replacement node can never start one.
@test "it imports every build of the lab's apps onto the new node, not only those in use" {
  run bash "$SCRIPT" k3d-lab-agent-0
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  assert_called k3d "image import go-api:latest -c lab"
  assert_called k3d "image import go-api:v1.2.0 -c lab"
  [ "$(call_count k3d "image import go-api:v1.2.0")" -eq 1 ]
  refute_called k3d "node-exporter"
}

@test "it fails, saying where to look, when the API server never comes back" {
  READYZ_FAILURES=1000 API_WAIT_SECONDS=10 run bash "$SCRIPT" k3d-lab-agent-0
  [ "$status" -ne 0 ]
  [[ "$output" == *"API server did not answer within 10s"* ]] || false
  [[ "$output" == *"docker ps --filter name=k3d-lab-serverlb"* ]] || false
}

@test "it refuses a node that is not cordoned" {
  stub_script kubectl <<'STUB'
case "$*" in
  *"node-role.kubernetes.io/control-plane"*) echo "v1.33.6+k3s1" ;;
  *"kubeletVersion"*) echo "v1.33.6+k3s1" ;;
  *"unschedulable"*) echo "" ;;
esac
exit 0
STUB
  run bash "$SCRIPT" k3d-lab-agent-0
  [ "$status" -ne 0 ]
  [[ "$output" == *"is not cordoned"* ]] || false
  refute_called k3d "node delete"
}

# A node must never come back older than it was, a patch release included.
@test "it refuses a patch downgrade within the same minor version" {
  KUBELET_VERSION=v1.33.13+k3s1 run bash "$SCRIPT" k3d-lab-agent-0
  [ "$status" -ne 0 ]
  [[ "$output" == *"refusing to downgrade k3d-lab-agent-0"* ]] || false
  refute_called k3d "node delete"
}

@test "it rolls a node onto a newer patch of its minor version" {
  TARGET_K3S_VERSION=v1.33.13-k3s1 run bash "$SCRIPT" k3d-lab-agent-0
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  assert_called k3d "--image rancher/k3s:v1.33.13-k3s1"
  [[ "$output" != *"newer than the"* ]] || false
}
