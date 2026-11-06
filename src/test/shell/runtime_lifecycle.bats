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
  # The stubs answer at once, so nothing needs up.sh's real-world waits.
  export REACHABLE_WAIT=0
}

teardown() {
  stub_teardown
}

# docker_recovers_after_restart makes `docker top` show k3s only once a node
# has been restarted, the way a real node recovers.
docker_recovers_after_restart() {
  mv "$STUB_BIN/docker" "$STUB_BIN/docker.rec"
  {
    echo '#!/usr/bin/env bash'
    echo "out=\"\$(\"$STUB_BIN/docker.rec\" \"\$@\")\"; code=\$?"
    echo 'case "$1" in'
    echo "  restart) touch \"$STUB_DIR/restarted\" ;;"
    echo "  top) [ -f \"$STUB_DIR/restarted\" ] && out=\"root 1 /bin/k3s agent\" ;;"
    echo 'esac'
    echo '[ -n "$out" ] && printf "%s\n" "$out"'
    echo 'exit $code'
  } >"$STUB_BIN/docker"
  chmod +x "$STUB_BIN/docker"
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

@test "k3d up gives each node an even share of Docker's memory" {
  stub_when k3d "cluster list" 1 # not found
  stub_when docker "MemTotal" 0 "8589934592" # 8 GiB
  AGENTS=1 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  # Two nodes of 4096 MiB each: every kubelet holds back the other 4096 MiB.
  # assert_called reads a grep pattern, hence the escaped node filter.
  assert_called k3d '--k3s-arg --kubelet-arg=system-reserved=memory=4096Mi@server:\*;agent:\*'
}

@test "k3d up splits Docker's memory over the server and every agent" {
  stub_when k3d "cluster list" 1 # not found
  stub_when docker "MemTotal" 0 "8589934592" # 8 GiB
  AGENTS=2 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  # Three nodes of 2730 MiB each.
  assert_called k3d 'system-reserved=memory=5462Mi@server:\*;agent:\*'
}

@test "k3d up reserves nothing for a single-node cluster" {
  stub_when k3d "cluster list" 1 # not found
  stub_when docker "MemTotal" 0 "8589934592"
  AGENTS=0 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called k3d "cluster create"
  refute_called k3d "system-reserved"
}

@test "k3d up creates the cluster with a warning when Docker's memory cannot be read" {
  stub_when k3d "cluster list" 1 # not found
  AGENTS=1 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called k3d "cluster create"
  refute_called k3d "system-reserved"
  [[ "$output" == *"could not read Docker's memory"* ]] || false
}

@test "k3d up leaves an existing cluster's memory settings alone" {
  stub_when docker "MemTotal" 0 "8589934592"
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called k3d "system-reserved"
}

@test "k3d up never deletes an existing cluster whose API does not answer" {
  # An existing cluster holds the user's apps and scenarios. up.sh restarts it
  # in order and, if it still does not answer, stops and leaves rebuilding to
  # the user.
  stub_when kubectl "get --raw" 1 # /healthz probe fails
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 1 ]
  [[ "$output" == *"labctl reset"* ]] || false
  # It tries the ordered restart k3d provides (servers, then agents) first.
  assert_called k3d "cluster stop testcluster"
  assert_called k3d "cluster start testcluster"
  refute_called k3d "cluster delete"
  refute_called k3d "cluster create"
}

@test "k3d up skips creation when an existing cluster is healthy" {
  # Present and reachable: no destructive delete, no recreate.
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called k3d "cluster delete"
  refute_called k3d "cluster create"
}

@test "k3d up repairs a load balancer that names a node that no longer exists" {
  # A removed agent stays in k3d's load balancer config. After a Docker restart
  # nginx fails on it and the API behind the load balancer never answers.
  stub_script docker <<STUB
case "\$*" in
  "ps -a --filter label=k3d.cluster=testcluster "*)
    printf 'k3d-testcluster-server-0 server\nk3d-testcluster-agent-1-0 agent\n' ;;
  "cp k3d-testcluster-serverlb:/etc/confd/values.yaml "*)
    printf 'ports:\n  80.tcp:\n  - k3d-testcluster-server-0\n  - k3d-testcluster-agent-0-0\n' >"\$3" ;;
  "cp "*" k3d-testcluster-serverlb:/etc/confd/values.yaml") cp "\$2" "$STUB_DIR/lb-values" ;;
esac
exit 0
STUB
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  [[ "$output" == *"k3d-testcluster-agent-0-0 no longer exists; the load balancer now uses k3d-testcluster-agent-1-0"* ]] || false
  [ "$(grep -cx '  - k3d-testcluster-agent-1-0' "$STUB_DIR/lb-values")" -eq 1 ]
  [ "$(grep -cx '  - k3d-testcluster-agent-0-0' "$STUB_DIR/lb-values")" -eq 0 ]
  assert_called docker "restart k3d-testcluster-serverlb"
  refute_called k3d "cluster delete"
}

@test "k3d up restarts a node container whose k3s has died" {
  # After a Docker/Colima restart the node containers come back with different
  # bridge IPs and k3s exits at once, but k3d's entrypoint keeps the container
  # Up - so the node sits NotReady and nothing self-heals. `init` must restart
  # it, not leave it or delete the cluster.
  stub_when docker "label=k3d.cluster" 0 "k3d-testcluster-agent-0"
  stub_when docker "k3d.role" 0 "agent"
  # `docker top` prints nothing until the node is restarted.
  docker_recovers_after_restart
  NODE_CHECK_INTERVAL=0 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called docker "restart k3d-testcluster-agent-0"
  assert_called kubectl "wait --for=condition=Ready node"
  refute_called k3d "cluster delete"
  refute_called k3d "cluster create"
}

@test "k3d up restarts a node that stays NotReady, and fails loudly if it never recovers" {
  # k3s can die a minute after a VM restart, after the first health check.
  stub_when kubectl "wait --for=condition=Ready node" 1
  stub_when kubectl "get nodes --no-headers" 0 "k3d-testcluster-agent-0   NotReady   <none>   1h   v1.33.6"
  NODE_READY_WAIT=0 NODE_CHECK_INTERVAL=0 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 1 ]
  assert_called docker "restart k3d-testcluster-agent-0"
  [[ "$output" == *"stay NotReady: k3d-testcluster-agent-0"* ]] || false
  refute_called k3d "cluster delete"
}

@test "k3d up restarts a node whose k3s dies late, and succeeds once it stays up" {
  stub_when docker "label=k3d.cluster" 0 "k3d-testcluster-agent-0"
  stub_when docker "k3d.role" 0 "agent"
  docker_recovers_after_restart
  NODE_STABLE_CHECKS=2 NODE_CHECK_INTERVAL=0 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_call_count docker 1 "restart k3d-testcluster-agent-0"
  refute_called k3d "cluster delete"
}

@test "k3d up corrects the recorded address of a node whose container moved" {
  # The Node records the address the container had before the restart; k3s
  # reads that one and shuts down on every start until the record is fixed.
  stub_when docker "label=k3d.cluster" 0 "k3d-testcluster-agent-0"
  stub_when docker "k3d.role" 0 "agent"
  stub_when docker "range .NetworkSettings.Networks" 0 "172.18.0.5"
  stub_when docker "InternalIP" 0 "172.18.0.2"
  docker_recovers_after_restart
  NODE_CHECK_INTERVAL=0 NODE_STABLE_CHECKS=1 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called docker "exec k3d-testcluster-server-0 kubectl --request-timeout=5s patch node k3d-testcluster-agent-0 --subresource=status"
  assert_called docker "172.18.0.5"
  refute_called kubectl "delete node"
  refute_called k3d "cluster delete"
}

@test "k3d up recovers a server whose address moved" {
  # A server with a stale recorded address shuts k3s down every few seconds,
  # and k3d's start waits on it, so its record is corrected from inside the
  # server while the start runs.
  stub_when docker "label=k3d.cluster" 0 "k3d-testcluster-server-0"
  stub_when docker "k3d.role" 0 "server"
  stub_when docker "range .NetworkSettings.Networks" 0 "172.18.0.3"
  stub_when docker "InternalIP" 0 "172.18.0.2"
  docker_recovers_after_restart
  NODE_CHECK_INTERVAL=0 NODE_STABLE_CHECKS=1 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  assert_called k3d "cluster start testcluster --timeout"
  assert_called docker "exec k3d-testcluster-server-0 kubectl --request-timeout=5s patch node k3d-testcluster-server-0 --subresource=status"
  assert_called kubectl "annotate node k3d-testcluster-server-0 --overwrite snowops.dev/address-corrected="
  refute_called k3d "cluster delete"
  refute_called k3d "cluster create"
}

@test "k3d up watches a just-started lab over several samples" {
  stub_when docker "label=k3d.cluster" 0 "k3d-testcluster-agent-0"
  stub_when docker "k3d.role" 0 "agent"
  stub_when docker "top" 0 "root 1 /bin/k3s agent"
  stub_when docker "{{.Status}}" 0 "Up 40 seconds"
  NODE_STABLE_CHECKS=3 NODE_CHECK_INTERVAL=0 run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  # lab_healthy looks once, then three stability samples.
  assert_call_count kubectl 4 "wait --for=condition=Ready node"
}

@test "k3d up leaves a node alone when k3s is running inside it" {
  stub_when docker "label=k3d.cluster" 0 "k3d-testcluster-agent-0"
  stub_when docker "k3d.role" 0 "agent"
  stub_when docker "top" 0 "root 1 /bin/k3s agent"
  run bash "$ROOT/runtimes/k3d/up.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called docker "restart"
}

@test "k3d down is a clean no-op when the cluster is absent" {
  stub_when k3d "cluster list" 1
  run bash "$ROOT/runtimes/k3d/down.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called k3d "cluster delete"
}

@test "k3d down removes a cluster's containers that k3d can no longer read" {
  # After an unclean stop k3d fails to read the cluster, yet its containers
  # remain and would collide with the next init. `docker ps` lists them until
  # `docker rm` removes them.
  stub_when k3d "cluster list" 1
  stub_when k3d "cluster delete" 1
  mv "$STUB_BIN/docker" "$STUB_BIN/docker.rec"
  {
    echo '#!/usr/bin/env bash'
    echo "\"$STUB_BIN/docker.rec\" \"\$@\""
    echo 'case "$1" in'
    echo "  rm) touch \"$STUB_DIR/removed\" ;;"
    echo "  ps) [ -f \"$STUB_DIR/removed\" ] || echo abc123 ;;"
    echo 'esac'
  } >"$STUB_BIN/docker"
  chmod +x "$STUB_BIN/docker"

  run bash "$ROOT/runtimes/k3d/down.sh" testcluster
  [ "$status" -eq 0 ]
  [[ "$output" != *"not found"* ]] || false
  assert_called docker "ps -aq --filter label=k3d.cluster=testcluster"
  assert_called docker "rm -f abc123"
  assert_called docker "network rm k3d-testcluster"
}

# --- kind -------------------------------------------------------------------

@test "kind up skips creation when the cluster already exists" {
  stub_stdout kind "testcluster" # `get clusters` lists it
  run bash "$ROOT/runtimes/kind/up.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called kind "create cluster"
}

@test "kind up never deletes an existing cluster whose API does not answer" {
  stub_stdout kind "testcluster" # `get clusters` lists it
  stub_when kubectl "get --raw" 1 # /healthz probe fails
  run bash "$ROOT/runtimes/kind/up.sh" testcluster
  [ "$status" -eq 1 ]
  [[ "$output" == *"labctl reset"* ]] || false
  refute_called kind "delete cluster"
  refute_called kind "create cluster"
}

@test "kind down is a clean no-op when the cluster is absent" {
  # Default kind stub prints nothing, so `get clusters` is empty => not found.
  run bash "$ROOT/runtimes/kind/down.sh" testcluster
  [ "$status" -eq 0 ]
  refute_called kind "delete cluster"
}
