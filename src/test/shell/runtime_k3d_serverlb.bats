#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# runtimes/_lib/k3d-serverlb.sh: k3d updates its load balancer's upstreams for
# servers only, so a replaced or removed agent stays listed. After the next
# Docker restart nginx fails on the missing host and the whole lab, API server
# included, is unreachable. serverlb_repair points the list at live nodes.

load 'helpers/stub'

setup() {
  stub_setup
  ROOT="$(project_root)"
  STATE="$STUB_DIR/state"
  mkdir -p "$STATE"
  export STATE
  stub_script docker <<'STUB'
case "$*" in
  "inspect k3d-lab-serverlb") [ ! -f "$STATE/no-lb" ] || exit 1 ;;
  "ps -a --filter label=k3d.cluster=lab "*) cat "$STATE/nodes" ;;
  "cp k3d-lab-serverlb:/etc/confd/values.yaml "*) cp "$STATE/values" "$3" ;;
  "cp "*" k3d-lab-serverlb:/etc/confd/values.yaml") cp "$2" "$STATE/written" ;;
  "restart k3d-lab-serverlb") touch "$STATE/restarted" ;;
esac
exit 0
STUB
  # The config k3d writes for one server and one agent, with ports 80 and 443
  # mapped on the load balancer.
  printf '%s\n' 'ports:' \
    '  80.tcp:' '  - k3d-lab-server-0' '  - k3d-lab-agent-0' \
    '  443.tcp:' '  - k3d-lab-server-0' '  - k3d-lab-agent-0' \
    '  6443.tcp:' '  - k3d-lab-server-0' \
    'settings:' '  workerConnections: 1024' >"$STATE/values"
  # shellcheck source=../../../runtimes/_lib/k3d-serverlb.sh
  . "$ROOT/runtimes/_lib/k3d-serverlb.sh"
}

teardown() { stub_teardown; }

live_nodes() { printf '%s\n' "$@" >"$STATE/nodes"; }

@test "it replaces an agent that no longer exists with the live one" {
  live_nodes 'k3d-lab-server-0 server' 'k3d-lab-agent-1-0 agent' 'k3d-lab-serverlb loadbalancer'
  run serverlb_repair lab
  [ "$status" -eq 0 ]
  [ -f "$STATE/restarted" ]
  [[ "$output" == *"k3d-lab-agent-0 no longer exists; the load balancer now uses k3d-lab-agent-1-0"* ]] || false
  [ "$(grep -c '^  - k3d-lab-agent-1-0$' "$STATE/written")" -eq 2 ]
  refute_written_line '  - k3d-lab-agent-0'
  # The API server's port and the settings are kept as they were.
  [ "$(sed -n '/^  6443.tcp:$/,/^settings:$/p' "$STATE/written")" = "$(printf '  6443.tcp:\n  - k3d-lab-server-0\nsettings:')" ]
  grep -q '^  workerConnections: 1024$' "$STATE/written"
}

@test "it leaves a config that names only live nodes alone" {
  live_nodes 'k3d-lab-server-0 server' 'k3d-lab-agent-0 agent'
  run serverlb_repair lab
  [ "$status" -eq 0 ]
  [ -z "$output" ]
  [ ! -f "$STATE/written" ]
  refute_called docker "restart"
}

@test "it only drops the missing node when the live agents are already listed" {
  # Node names are prefixes of one another: agent-0 is gone, agent-0-0 is not.
  printf '%s\n' 'ports:' '  80.tcp:' '  - k3d-lab-agent-0' '  - k3d-lab-agent-0-0' \
    '  6443.tcp:' '  - k3d-lab-server-0' >"$STATE/values"
  live_nodes 'k3d-lab-server-0 server' 'k3d-lab-agent-0-0 agent'
  run serverlb_repair lab
  [ "$status" -eq 0 ]
  [[ "$output" == *"k3d-lab-agent-0 no longer exists; removed it from the load balancer"* ]] || false
  [ "$(cat "$STATE/written")" = "$(printf 'ports:\n  80.tcp:\n  - k3d-lab-agent-0-0\n  6443.tcp:\n  - k3d-lab-server-0')" ]
}

@test "it falls back to the servers rather than leave a port with no upstream" {
  printf '%s\n' 'ports:' '  80.tcp:' '  - k3d-lab-agent-0' \
    '  6443.tcp:' '  - k3d-lab-server-0' >"$STATE/values"
  live_nodes 'k3d-lab-server-0 server'
  run serverlb_repair lab
  [ "$status" -eq 0 ]
  [ "$(sed -n 2,3p "$STATE/written")" = "$(printf '  80.tcp:\n  - k3d-lab-server-0')" ]
}

@test "it changes nothing when it cannot list the cluster's nodes" {
  # An empty list would make every upstream look stale.
  : >"$STATE/nodes"
  run serverlb_repair lab
  [ "$status" -eq 1 ]
  [ ! -f "$STATE/written" ]
  refute_called docker "restart"
}

@test "a cluster without a load balancer is left alone" {
  touch "$STATE/no-lb"
  run serverlb_repair lab
  [ "$status" -eq 0 ]
  refute_called docker "cp"
}

refute_written_line() {
  if grep -qx -- "$1" "$STATE/written"; then
    printf 'did not expect the line "%s" in:\n%s\n' "$1" "$(cat "$STATE/written")" >&2
    return 1
  fi
}
