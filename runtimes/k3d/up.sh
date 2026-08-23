#!/usr/bin/env bash
set -euo pipefail

# Create a k3d cluster
# Expose HTTP/HTTPS ports on the host so that ingress rules using
# hostnames resolve to localhost and traffic reaches the load-balancer pod.
# Values come from .env (via labctl config) or environment variables.

CLUSTER_NAME="${1:-${CLUSTER_NAME:-snowops}}"
HTTP_PORT="${HTTP_PORT:-80}"
HTTPS_PORT="${HTTPS_PORT:-443}"
# Number of agent (worker) nodes. Multi-node by default so day-2 drills
# (node drain, rolling upgrade — task 060) have somewhere to reschedule pods.
AGENTS="${AGENTS:-2}"
# Optional k3s version pin (e.g. K3S_VERSION=v1.28.8-k3s1). Empty = k3d default.
# The cluster-upgrade-drill creates a cluster pinned to an older version, then
# rolls the agents to a newer one.
K3S_VERSION="${K3S_VERSION:-}"

# ---------------------------------------------------------------------------
# Docker daemon readiness — auto-start Colima on macOS if needed
# ---------------------------------------------------------------------------
ensure_docker() {
  if docker info &>/dev/null; then
    return 0
  fi

  echo "Docker daemon not reachable. Checking for a container runtime..."

  if command -v colima &>/dev/null; then
    echo "Starting Colima..."
    colima start
    local retries=0
    until docker info &>/dev/null; do
      retries=$((retries + 1))
      if [ "$retries" -ge 30 ]; then
        echo "ERROR: Docker daemon still not reachable after 30s." >&2
        echo "       Run 'colima status' for details." >&2
        exit 1
      fi
      sleep 1
    done
    echo "Colima started and Docker daemon is ready."
    return 0
  fi

  echo "ERROR: Docker daemon is not running and no supported runtime was found." >&2
  echo "  macOS options (pick one):" >&2
  echo "    colima:         brew install colima && colima start" >&2
  echo "    Docker Desktop: https://www.docker.com/products/docker-desktop" >&2
  echo "    OrbStack:       https://orbstack.dev" >&2
  exit 1
}

ensure_docker

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
    echo "Rewriting API server ${server} -> ${fixed} for reliable access on macOS."
    kubectl config set-cluster "$ctx" --server="$fixed" >/dev/null
  fi
}

# Return 0 only if the cluster's API server actually answers. This is what tells
# a healthy existing cluster (skip creation) apart from a half-built one whose
# containers linger but whose API never comes up — e.g. a load-balancer that
# failed to initialise, or a kubeconfig entry that was removed out from under it.
cluster_reachable() {
  kubectl config use-context "k3d-$CLUSTER_NAME" >/dev/null 2>&1 || return 1
  kubectl --request-timeout=20s get --raw=/healthz >/dev/null 2>&1
}

# port_free <port> — 0 if nothing is listening on the host TCP port, non-zero if
# it is already taken. Uses bash's /dev/tcp so it needs no nc/lsof/ss (which
# differ across macOS and Linux — golden rule 1). The subshell scopes fd 3.
port_free() {
  ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

# pick_port <preferred> <fallback-base> — echo <preferred> if it is free, else
# the first free port at or above <fallback-base>. Lets a second cluster come up
# on one host: k3d's load-balancer cannot bind a host port another cluster (or
# any service) already holds, and would otherwise sit unstarted forever.
pick_port() {
  local preferred=$1 base=$2 p
  if port_free "$preferred"; then
    printf '%s' "$preferred"
    return 0
  fi
  p=$base
  while ! port_free "$p"; do
    p=$((p + 1))
    if [ "$p" -gt $((base + 100)) ]; then
      echo "ERROR: no free host port found near ${base} for ingress." >&2
      return 1
    fi
  done
  printf '%s' "$p"
}

# Raise fs.inotify.max_user_instances on every node. The default (128) is quickly
# exhausted by log/file watchers — most visibly promtail (observability-sre),
# which crash-loops with "too many open files: failed to make file target
# manager". k3d nodes run privileged and share the colima/Docker VM kernel, so
# setting the sysctl on each node container raises the effective limit for the
# whole cluster. Best-effort: never fail cluster bringup over this.
raise_inotify_limits() {
  local node
  for node in $(docker ps --filter "label=k3d.cluster=${CLUSTER_NAME}" --format '{{.Names}}' 2>/dev/null); do
    docker exec "$node" sysctl -w fs.inotify.max_user_instances=512 >/dev/null 2>&1 || true
  done
}

# Disable the bundled Traefik so we manage our own install in the traefik namespace.
# This prevents two competing Traefik instances from causing 404 errors.
create_cluster() {
  # Fall back to free host ports when the defaults are already bound (e.g. another
  # k3d cluster is running). Keeps the golden path on 80/443 untouched.
  local http_port https_port
  http_port="$(pick_port "$HTTP_PORT" 8080)" || exit 1
  https_port="$(pick_port "$HTTPS_PORT" 8443)" || exit 1
  if [ "$http_port" != "$HTTP_PORT" ] || [ "$https_port" != "$HTTPS_PORT" ]; then
    echo "Host ports ${HTTP_PORT}/${HTTPS_PORT} are already in use (another cluster or service)."
    echo "Exposing ingress on ${http_port}/${https_port} instead — reach services at" \
      "http://<name>.${DOMAIN_SUFFIX:-k3d.local}:${http_port}"
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

  k3d cluster create "${create_args[@]}"

  kubectl config use-context "k3d-$CLUSTER_NAME"
  normalize_apiserver_host
}

# An existing cluster is only worth keeping if its API answers. k3d can leave a
# cluster half-built (containers present but the load-balancer or kubeconfig
# broken). The old code assumed "exists" meant "usable" and ran
# `kubectl config use-context` against a context that no longer existed, which
# aborts `make init` under `set -e`. Instead: refresh the kubeconfig entry, and
# skip creation only when the API is reachable. A broken cluster is recreated
# rather than left to fail every step that follows.
if k3d cluster list "$CLUSTER_NAME" &>/dev/null; then
  echo "Cluster '$CLUSTER_NAME' already exists — verifying it is reachable."
  # Rebuild the kubeconfig entry in case it was removed or points at a stale port.
  k3d kubeconfig merge "$CLUSTER_NAME" --kubeconfig-merge-default &>/dev/null || true
  normalize_apiserver_host
  if cluster_reachable; then
    echo "Cluster '$CLUSTER_NAME' is healthy; skipping creation."
    raise_inotify_limits
    exit 0
  fi
  echo "Cluster '$CLUSTER_NAME' exists but its API is not reachable — recreating it."
  k3d cluster delete "$CLUSTER_NAME" &>/dev/null || true
fi

create_cluster
raise_inotify_limits
