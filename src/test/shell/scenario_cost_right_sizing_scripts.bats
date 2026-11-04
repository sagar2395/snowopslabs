#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# cost-right-sizing's inflate.sh over-provisions the shared workload. The
# inflated pod has to fit on a node, or the rollout waits five minutes and the
# scenario never activates, so the size comes from the room the nodes have.

load 'helpers/stub'

setup() {
  stub_setup
  ROOT="$(project_root)"
  SCRIPT="$ROOT/scenarios/cost-right-sizing/scripts/inflate.sh"
  export WORKLOAD_NAME=go-api WORKLOAD_NAMESPACE=go-api
  # name, allocatable cpu, allocatable memory, unschedulable, taint effects
  export NODES=""
  # node, deletion time, cpu requests, memory requests (one entry per container)
  export PODS=""
  stub_script kubectl <<'EOF'
case "$*" in
  *"get nodes"*) printf '%b' "$NODES" ;;
  *"get pods"*) printf '%b' "$PODS" ;;
esac
exit 0
EOF
}

teardown() { stub_teardown; }

@test "a 2 CPU node with the platform on it gets a request that fits" {
  NODES='k3d-server\t2\t4014080Ki\t\t\n'
  PODS='k3d-server\t\t100m,250m,\t128Mi,512Mi,\nk3d-server\t\t0.5,\t1Gi,\nk3d-server\t\t100m,\t64Mi,\n'
  run bash "$SCRIPT"
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  assert_called kubectl "set resources deployment go-api --requests=cpu=700m,memory=1024Mi --limits=cpu=700m,memory=1024Mi"
}

@test "a roomy node gets the full 2000m and 1Gi" {
  NODES='kind-control-plane\t8\t16Gi\t\t\n'
  PODS='kind-control-plane\t\t250m,\t256Mi,\n'
  run bash "$SCRIPT"
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  assert_called kubectl "--requests=cpu=2000m,memory=1024Mi --limits=cpu=2000m,memory=1024Mi"
}

@test "sizes from the node with the most free CPU, skipping cordoned and tainted nodes" {
  NODES='cordoned\t16\t32Gi\ttrue\t\ntainted\t16\t32Gi\t\tNoSchedule\nbusy\t2\t4Gi\t\t\nfree\t1900m\t2Gi\t\t\n'
  PODS='busy\t\t1500m,\t1Gi,\nfree\t\t100m,200m,\t512M,600Mi,\n\t\t4,\t8Gi,\n'
  run bash "$SCRIPT"
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  # "free" keeps 1600m and about 960Mi unrequested; the Pending pod has no node.
  assert_called kubectl "--requests=cpu=1200m,memory=704Mi"
}

@test "memory is sized down when the node is short of it" {
  NODES='k3d-server\t4\t2Gi\t\t\n'
  PODS='k3d-server\t\t100m,\t1Gi,\n'
  run bash "$SCRIPT"
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  assert_called kubectl "--requests=cpu=2000m,memory=768Mi"
}

@test "a full node fails at once and changes nothing" {
  NODES='k3d-server\t2\t4Gi\t\t\n'
  PODS='k3d-server\t\t1600m,\t1Gi,\n'
  run bash "$SCRIPT"
  [ "$status" -ne 0 ]
  [[ "$output" == *"400m CPU"* ]] || { echo "$output"; return 1; }
  [[ "$output" == *"docs/resources.md"* ]] || { echo "$output"; return 1; }
  refute_called kubectl "annotate"
  refute_called kubectl "set resources"
  assert_call_count kubectl 1 "rollout status"
}

@test "a node short of memory fails at once and changes nothing" {
  NODES='k3d-server\t4\t2Gi\t\t\n'
  PODS='k3d-server\t\t100m,\t1600Mi,\n'
  run bash "$SCRIPT"
  [ "$status" -ne 0 ]
  [[ "$output" == *"448Mi memory"* ]] || { echo "$output"; return 1; }
  refute_called kubectl "set resources"
}

@test "a terminating pod's requests do not count" {
  NODES='k3d-server\t2\t4Gi\t\t\n'
  PODS='k3d-server\t2026-11-04T04:12:00Z\t900m,\t1Gi,\nk3d-server\t\t50m,\t32Mi,\n'
  run bash "$SCRIPT"
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  assert_called kubectl "--requests=cpu=1400m,memory=1024Mi"
}

@test "waits for a rollout in progress before reading the nodes" {
  NODES='k3d-server\t4\t4Gi\t\t\n'
  run bash "$SCRIPT"
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  first=$(calls_for kubectl | grep -n -e "rollout status" -e "get nodes" | head -1)
  [[ "$first" == *"rollout status deployment go-api --timeout=2m"* ]] || { calls_for kubectl; return 1; }
}

@test "INFLATED_ overrides win and skip reading the nodes" {
  export INFLATED_CPU_REQUEST=3 INFLATED_MEM_REQUEST=2Gi
  run bash "$SCRIPT"
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  refute_called kubectl "get nodes"
  assert_called kubectl "--requests=cpu=3,memory=2Gi --limits=cpu=3,memory=2Gi"
}
