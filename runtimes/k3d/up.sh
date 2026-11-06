#!/usr/bin/env bash
set -euo pipefail

# Create a k3d cluster
# Expose HTTP/HTTPS ports on the host so that ingress rules using
# hostnames resolve to localhost and traffic reaches the load-balancer pod.
# Values come from .env (via labctl config) or environment variables.

CLUSTER_NAME="${1:-${CLUSTER_NAME:-snowops}}"
HTTP_PORT="${HTTP_PORT:-80}"
HTTPS_PORT="${HTTPS_PORT:-443}"
# Number of agent (worker) nodes. One agent plus the server gives two
# schedulable nodes; drills that need more add them when they start
# (add-agents.sh) and remove them when they end (remove-agents.sh).
AGENTS="${AGENTS:-1}"
# Optional k3s version pin (e.g. K3S_VERSION=v1.28.8-k3s1). Empty = k3d default.
# The cluster-upgrade-drill creates a cluster pinned to an older version, then
# rolls the agents to a newer one.
K3S_VERSION="${K3S_VERSION:-}"
# How long to wait for an existing cluster's API after a restart, per attempt.
REACHABLE_WAIT="${REACHABLE_WAIT:-180}"
# How long to keep waiting (and restarting stuck nodes) for every node to be Ready.
NODE_READY_WAIT="${NODE_READY_WAIT:-300}"
# Consecutive healthy samples, NODE_CHECK_INTERVAL seconds apart, before a
# restarted cluster counts as settled.
NODE_STABLE_CHECKS="${NODE_STABLE_CHECKS:-6}"
NODE_CHECK_INTERVAL="${NODE_CHECK_INTERVAL:-10}"

# shellcheck source=../_lib/docker.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/_lib/docker.sh"
# shellcheck source=../_lib/k3d-serverlb.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/_lib/k3d-serverlb.sh"

ensure_docker_running || exit 1

# ---------------------------------------------------------------------------
# Normalise the kubeconfig API-server host.
#
# k3d writes the server as https://0.0.0.0:<port>. 0.0.0.0 is a bind-all
# address, not a stable destination: on macOS (Docker Desktop / Colima) a
# client connecting to it intermittently stalls the TLS handshake, which
# surfaces as "TLS handshake timeout" / "http2: client connection lost" partway
# through a helm install. Rewriting the host to 127.0.0.1 is reliable on macOS
# and Linux alike (golden rule 1).
# ---------------------------------------------------------------------------
normalize_apiserver_host() {
  local ctx="k3d-$CLUSTER_NAME" server fixed
  server="$(kubectl config view -o jsonpath="{.clusters[?(@.name=='${ctx}')].cluster.server}" 2>/dev/null || true)"
  [ -n "$server" ] || return 0
  fixed="${server//0.0.0.0/127.0.0.1}"
  if [ "$server" != "$fixed" ]; then
    echo "Pointing kubectl at ${fixed} (k3d wrote ${server})."
    kubectl config set-cluster "$ctx" --server="$fixed" >/dev/null
  fi
}

# Return 0 only if the cluster's API server actually answers. This is what tells
# a healthy existing cluster (skip creation) apart from a half-built one whose
# containers linger but whose API never comes up - e.g. a load-balancer that
# failed to initialise, or a kubeconfig entry that was removed out from under it.
cluster_reachable() {
  kubectl config use-context "k3d-$CLUSTER_NAME" >/dev/null 2>&1 || return 1
  kubectl --request-timeout=20s get --raw=/healthz >/dev/null 2>&1
}

# Raise fs.inotify.max_user_instances on every node. The default (128) is quickly
# exhausted by log/file watchers - most visibly promtail (observability-sre),
# which crash-loops with "too many open files: failed to make file target
# manager". k3d nodes run privileged and share the colima/Docker VM kernel, so
# setting the sysctl on each node container raises the effective limit for the
# whole cluster. Best-effort: never fail cluster bringup over this.
raise_inotify_limits() {
  local node
  # k3d nodes share the kernel of whatever runs Docker. Under colima, Docker
  # Desktop or WSL that is a VM; on native Linux it is this machine.
  if [ "$(uname -s)" = "Linux" ] && [ -z "${WSL_DISTRO_NAME:-}" ]; then
    echo "Raising fs.inotify.max_user_instances to 512 (on native Linux this is your host's kernel setting)."
  fi
  for node in $(docker ps --filter "label=k3d.cluster=${CLUSTER_NAME}" --format '{{.Names}}' 2>/dev/null); do
    docker exec "$node" sysctl -w fs.inotify.max_user_instances=512 >/dev/null 2>&1 || true
  done
}

# node_containers prints the cluster's server and agent containers, the ones
# that run k3s; the load balancer and k3d's helper containers are left out.
node_containers() {
  local node role
  for node in $(docker ps --filter "label=k3d.cluster=${CLUSTER_NAME}" --format '{{.Names}}' 2>/dev/null); do
    role="$(docker inspect "$node" --format '{{index .Config.Labels "k3d.role"}}' 2>/dev/null || true)"
    case "$role" in
      server | agent) printf '%s\n' "$node" ;;
    esac
  done
}

# dead_nodes prints the node containers with no k3s process in them. When k3s
# exits, k3d's entrypoint keeps looping on `kubectl uncordon`, so the container
# stays Up, Docker never restarts it, and the node sits NotReady until it is
# restarted.
dead_nodes() {
  local node
  for node in $(node_containers); do
    # Matched on a captured string, not through a pipe: `set -o pipefail` plus
    # grep -q's early exit reports the whole pipeline as failed on a match.
    case "$(docker top "$node" 2>/dev/null || true)" in
      */bin/k3s*) ;;
      *) printf '%s\n' "$node" ;;
    esac
  done
}

# wait_until_reachable <seconds> - succeed once the API answers through the
# host's kubeconfig, which goes through k3d's load balancer.
wait_until_reachable() {
  local limit="$1" waited=0
  until cluster_reachable; do
    if [ "$waited" -ge "$limit" ]; then
      return 1
    fi
    sleep 5
    waited=$((waited + 5))
  done
}

# server_kubectl runs kubectl inside the first server container. It reaches the
# API directly, so it works before k3d has started the load balancer the host's
# kubeconfig goes through.
server_kubectl() {
  docker exec "k3d-${CLUSTER_NAME}-server-0" kubectl --request-timeout=5s "$@"
}

# correct_node_address <node> - when the Node object records an address other
# than the one the node's container has now, set the record to the current
# address. After Docker or the colima VM restarts, containers can come back with
# each other's addresses, and k3s's network policy controller reads the recorded
# one at start: with no interface holding it, k3s shuts down on every start
# ("failed to find interface with specified node ip"), a server included.
# Correcting the record keeps the Node, its name and its pods, and needs the API
# for a moment only. Returns 0 when the record is right or was corrected (the
# node is then added to CORRECTED), 1 when it could not be read or written.
CORRECTED=""
correct_node_address() {
  local node="$1" have recorded patch
  have="$(docker inspect "$node" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' 2>/dev/null || true)"
  recorded="$(server_kubectl get node "$node" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || true)"
  if [ -z "$have" ] || [ -z "$recorded" ]; then
    return 1
  fi
  [ "$have" = "$recorded" ] && return 0
  echo "Node '$node' is recorded at ${recorded} but its container now has ${have} - correcting the record."
  patch="[{\"op\":\"replace\",\"path\":\"/status/addresses\",\"value\":[{\"type\":\"InternalIP\",\"address\":\"${have}\"},{\"type\":\"Hostname\",\"address\":\"${node}\"}]}]"
  server_kubectl patch node "$node" --subresource=status --type=json -p "$patch" >/dev/null 2>&1 || return 1
  CORRECTED="$CORRECTED$node "
}

# correct_addresses <seconds> - while a server's recorded address is stale its
# API answers only for moments between k3s shutdowns, so keep checking every
# node whenever it answers. Succeeds once one pass has read every node's record
# and found each right; fails only if the API never answered within the time.
correct_addresses() {
  local deadline=$((SECONDS + $1)) answered=0 settled before node
  while :; do
    if server_kubectl get --raw=/healthz >/dev/null 2>&1; then
      answered=1
      settled=1
      before="$CORRECTED"
      for node in $(node_containers); do
        correct_node_address "$node" || settled=0
      done
      [ "$CORRECTED" != "$before" ] && settled=0
      [ "$settled" -eq 1 ] && return 0
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      [ "$answered" -eq 1 ]
      return
    fi
    sleep 2
  done
}

# mark_corrected annotates each corrected Node once the cluster is stable. The
# annotation records the correction, and changing a Node's metadata makes k3s
# rewrite CoreDNS's node host entries, which the status correction alone does
# not; while k3s is still restarting, that rewrite would be lost.
mark_corrected() {
  local node stamp
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  for node in $CORRECTED; do
    kubectl annotate node "$node" --overwrite "snowops.dev/address-corrected=${stamp}" >/dev/null 2>&1 || true
  done
}

# recently_started reports whether a node container has been up for less than
# five minutes, the window in which k3s can still die after looking Ready.
recently_started() {
  local node
  for node in $(node_containers); do
    case "$(docker ps --filter "name=^${node}\$" --format '{{.Status}}' 2>/dev/null)" in
      "Up Less than a second"* | "Up "[0-9]*" second"* | "Up About a minute"* | "Up "[1-4]" minute"*) return 0 ;;
    esac
  done
  return 1
}

# ensure_nodes_ready waits until every node runs k3s and is Ready. Just after
# node containers (re)start, a node's k3s can die a minute in while its Node
# still reads Ready, so then it must stay Ready for NODE_STABLE_CHECKS samples
# NODE_CHECK_INTERVAL seconds apart; a lab up for a while needs one look. A
# node found dead or NotReady has its recorded address corrected and is
# restarted, at most twice.
ensure_nodes_ready() {
  local deadline=$((SECONDS + NODE_READY_WAIT)) stable=0 need=1 node bad restarts=""
  recently_started && need="$NODE_STABLE_CHECKS"
  echo "Waiting for every node to be Ready and stay up..."
  while :; do
    bad="$(dead_nodes)"
    if [ -z "$bad" ] && kubectl wait --for=condition=Ready node --all --timeout=20s >/dev/null 2>&1; then
      stable=$((stable + 1))
      [ "$stable" -ge "$need" ] && return 0
    else
      stable=0
      bad="$bad $(kubectl get nodes --no-headers 2>/dev/null | awk '$2 !~ /^Ready/ {print $1}')"
      for node in $bad; do
        # restarts lists a node once per restart; allow two per node.
        [ "$(printf '%s\n' $restarts | grep -cx "$node" || true)" -ge 2 ] && continue
        restarts="$restarts$node "
        correct_node_address "$node" || true
        echo "Node '$node' is not healthy - restarting it."
        docker restart "$node" >/dev/null 2>&1 || true
        need="$NODE_STABLE_CHECKS"
      done
    fi
    [ "$SECONDS" -ge "$deadline" ] && return 1
    sleep "$NODE_CHECK_INTERVAL"
  done
}

# k3d_images prints the images `k3d cluster create` will need: the k3s node
# image (pinned or k3d's default) and k3d's own helper images.
k3d_images() {
  local out k3d_ver k3s_ver
  out="$(k3d version 2>/dev/null || true)"
  k3d_ver="$(printf '%s\n' "$out" | sed -n 's/^k3d version \(v[^ ]*\).*/\1/p' | head -n 1)"
  k3s_ver="${K3S_VERSION:-$(printf '%s\n' "$out" | sed -n 's/^k3s version \(v[^ ]*\).*/\1/p' | head -n 1)}"
  [ -n "$k3s_ver" ] && printf 'rancher/k3s:%s\n' "$k3s_ver"
  if [ -n "$k3d_ver" ]; then
    printf 'ghcr.io/k3d-io/k3d-tools:%s\n' "${k3d_ver#v}"
    printf 'ghcr.io/k3d-io/k3d-proxy:%s\n' "${k3d_ver#v}"
  fi
  return 0
}

# Disable the bundled Traefik so we manage our own install in the traefik namespace.
# This prevents two competing Traefik instances from causing 404 errors.
create_cluster() {
  if name_used_by kind "$CLUSTER_NAME"; then
    echo "ERROR: a kind cluster named '$CLUSTER_NAME' already exists on this machine." >&2
    echo "  Lab names are unique per machine. Set CLUSTER_NAME to another name in .env, then 'labctl init'." >&2
    exit 1
  fi
  # Fall back to free host ports when the defaults are already bound (e.g. another
  # k3d cluster is running). Keeps the golden path on 80/443 untouched.
  local http_port https_port
  http_port="$(pick_port "$HTTP_PORT" 8080)" || exit 1
  https_port="$(pick_port "$HTTPS_PORT" 8443)" || exit 1
  if [ "$http_port" != "$HTTP_PORT" ] || [ "$https_port" != "$HTTPS_PORT" ]; then
    echo "Host ports ${HTTP_PORT}/${HTTPS_PORT} are already in use (another cluster or service)."
    echo "Exposing ingress on ${http_port}/${https_port} instead - reach services at" \
      "http://<name>.${DOMAIN_SUFFIX:-${CLUSTER_NAME}.localhost}:${http_port}"
    HTTP_PORT="$http_port"
    HTTPS_PORT="$https_port"
  fi

  local create_args=(
    "$CLUSTER_NAME"
    --agents "$AGENTS"
    -p "${HTTP_PORT}:80@loadbalancer"
    -p "${HTTPS_PORT}:443@loadbalancer"
    --k3s-arg "--disable=traefik@server:*"
  )
  if [ -n "$K3S_VERSION" ]; then
    echo "Pinning k3s version to ${K3S_VERSION}"
    create_args+=(--image "rancher/k3s:${K3S_VERSION}")
  fi
  # Each node offers the scheduler an even share of Docker's memory rather than
  # all of it. An existing cluster keeps its settings until it is recreated.
  local reserved
  reserved="$(node_memory_reserved $((AGENTS + 1)))"
  if [ -n "$reserved" ]; then
    create_args+=(--k3s-arg "--kubelet-arg=system-reserved=memory=${reserved}Mi@server:*;agent:*")
  elif [ "$AGENTS" -gt 0 ]; then
    echo "WARNING: could not read Docker's memory, so each node offers all of it to the scheduler." >&2
    echo "  'labctl doctor' warns when the pods' memory requests come close to Docker's memory." >&2
  fi

  # Pull the images first, with progress and a stall timeout: k3d pulls them
  # silently, so a stalled download would otherwise look like a hang.
  echo "Downloading cluster images (first run only, a few hundred MB)..."
  local images=() img
  while IFS= read -r img; do
    [ -n "$img" ] && images+=("$img")
  done < <(k3d_images)
  # The +-expansion keeps bash 3.2 (macOS) quiet about an empty array under set -u.
  prepull_images ${images[@]+"${images[@]}"} || exit 1

  # --timeout rolls back a creation that never finishes; one retry covers a
  # transient failure without leaving a half-built cluster behind.
  if ! k3d cluster create "${create_args[@]}" --timeout 300s; then
    echo "Cluster creation failed - cleaning up and retrying once..." >&2
    k3d cluster delete "$CLUSTER_NAME" >/dev/null 2>&1 || true
    k3d cluster create "${create_args[@]}" --timeout 300s || {
      echo "ERROR: k3d could not create the cluster. 'labctl doctor' checks Docker's resources." >&2
      exit 1
    }
  fi

  kubectl config use-context "k3d-$CLUSTER_NAME"
  normalize_apiserver_host
}

# An existing cluster is brought back rather than replaced: its kubeconfig
# entry is refreshed, an unhealthy cluster is restarted in order, and every
# node must stay Ready before creation is skipped. If it still cannot be
# reached, the script stops and points at `labctl reset`.
# lab_healthy is the quick look: API answering, k3s in every node container,
# and every node Ready.
lab_healthy() {
  cluster_reachable &&
    [ -z "$(dead_nodes)" ] &&
    kubectl wait --for=condition=Ready node --all --timeout=20s >/dev/null 2>&1
}

# restart_in_order restarts the cluster the way k3d does it, servers before
# agents. k3d waits for every agent to register, which cannot happen while the
# server's recorded address is stale, so the records are corrected while the
# start runs.
restart_in_order() {
  local start_pid
  echo "Restarting the cluster in order (servers, then agents)..."
  k3d cluster stop "$CLUSTER_NAME" >/dev/null 2>&1 || true
  k3d cluster start "$CLUSTER_NAME" --timeout "${REACHABLE_WAIT}s" &
  start_pid=$!
  correct_addresses "$REACHABLE_WAIT" || true
  # After a correction the server has restarted, and k3d's last step keeps
  # waiting for a server log line until its timeout. Once the cluster answers
  # through the load balancer, which k3d starts before that step, it is done.
  if [ -n "$CORRECTED" ] && wait_until_reachable "$REACHABLE_WAIT"; then
    kill "$start_pid" 2>/dev/null || true
  fi
  wait "$start_pid" 2>/dev/null || true
  k3d kubeconfig merge "$CLUSTER_NAME" --kubeconfig-merge-default &>/dev/null || true
  normalize_apiserver_host
}

if k3d cluster list "$CLUSTER_NAME" &>/dev/null; then
  echo "Cluster '$CLUSTER_NAME' already exists - checking it is healthy."
  # Rebuild the kubeconfig entry in case it was removed or points at a stale port.
  k3d kubeconfig merge "$CLUSTER_NAME" --kubeconfig-merge-default &>/dev/null || true
  normalize_apiserver_host
  # A node removed since the last start can leave the load balancer naming a
  # container that is gone. After a Docker restart that keeps it restart-looping
  # and the API unreachable, and restarting the cluster does not help.
  repaired="$(serverlb_repair "$CLUSTER_NAME" || true)"
  if [ -n "$repaired" ]; then
    echo "The load balancer named nodes that no longer exist; repaired it:"
    echo "$repaired"
  fi
  if ! lab_healthy; then
    restart_in_order
  fi
  if ! correct_addresses "$REACHABLE_WAIT" || ! wait_until_reachable "$REACHABLE_WAIT"; then
    # The cluster holds the user's apps, scenarios and dashboards, so it is
    # left in place; `labctl reset` rebuilds it.
    echo "ERROR: cluster '$CLUSTER_NAME' exists but its API server is not answering." >&2
    echo "  Check Docker's memory with 'labctl doctor' and the nodes with 'docker ps'." >&2
    echo "  To rebuild the lab from scratch (this loses its apps and scenarios): labctl reset" >&2
    exit 1
  fi
  if ! ensure_nodes_ready; then
    echo "ERROR: some nodes of '$CLUSTER_NAME' stay NotReady: $(kubectl get nodes --no-headers 2>/dev/null | awk '$2 !~ /^Ready/ {printf "%s ", $1}')" >&2
    echo "  Check Docker's memory with 'labctl doctor'. To rebuild the lab from scratch (loses its state): labctl reset" >&2
    exit 1
  fi
  mark_corrected
  echo "Cluster '$CLUSTER_NAME' is healthy; skipping creation."
  raise_inotify_limits
  record_ingress_ports "$CLUSTER_NAME" "k3d-${CLUSTER_NAME}-serverlb"
  exit 0
fi

create_cluster
raise_inotify_limits
record_ingress_ports "$CLUSTER_NAME" "k3d-${CLUSTER_NAME}-serverlb"
