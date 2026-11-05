#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Docker engine helpers shared by bootstrap/setup-tools.sh and the local
# runtimes (k3d, kind). Source it; it defines functions only.
#
# Sizing contract: a stopped colima is started at LAB_CPUS/LAB_MEMORY (default
# 2 CPU / 4 GB), never smaller than it already is. A running engine is never
# resized here - labctl's preflight refuses an undersized one and prints how.

LAB_CPUS="${LAB_CPUS:-2}"
LAB_MEMORY="${LAB_MEMORY:-4}"

# wait_for_docker <seconds> - succeed once the daemon answers.
wait_for_docker() {
  local limit="${1:-60}" waited=0
  until docker info >/dev/null 2>&1; do
    if [ "$waited" -ge "$limit" ]; then
      echo "ERROR: the Docker daemon did not answer within ${limit}s." >&2
      return 1
    fi
    sleep 2
    waited=$((waited + 2))
  done
}

# colima_size_arg <field> <wanted> - the larger of colima's configured value
# for field (cpus | memory, memory in GiB) and wanted, so a stopped VM is only
# ever grown. Falls back to wanted when colima reports nothing.
colima_size_arg() {
  local field="$1" wanted="$2" have=""
  if command -v jq >/dev/null 2>&1; then
    have="$(colima list --json 2>/dev/null | jq -r --arg f "$field" \
      'select(.name == "default") | .[$f] // empty' 2>/dev/null | head -n 1)"
  fi
  case "$have" in
    '' | *[!0-9]*) printf '%s' "$wanted" ;;
    *)
      # colima reports memory in bytes.
      [ "$field" = memory ] && have=$((have / 1073741824))
      if [ "$have" -gt "${wanted%%.*}" ]; then printf '%s' "$have"; else printf '%s' "$wanted"; fi
      ;;
  esac
}

# docker_access_problem prints why a present docker CLI cannot use the daemon
# when the cause is not "it is stopped", and fails; it succeeds (prints
# nothing) otherwise. Two cases make every later step fail confusingly:
#   - Docker Desktop's WSL shim, when WSL Integration is off for this distro
#   - a user just added to the docker group whose session predates it
docker_access_problem() {
  local out
  command -v docker >/dev/null 2>&1 || return 0
  out="$(docker info 2>&1 >/dev/null || true)"
  case "$out" in
    *"could not be found in this WSL 2 distro"*)
      echo "Docker Desktop is installed on Windows but not enabled for this WSL distro."
      echo "  Docker Desktop → Settings → Resources → WSL Integration → enable '${WSL_DISTRO_NAME:-this distro}',"
      echo "  Apply & Restart, then reopen this terminal and re-run 'labctl init'."
      return 1
      ;;
    *"permission denied"*)
      echo "Docker is running, but your user may not use it yet."
      if id -nG 2>/dev/null | grep -qw docker; then
        echo "  You are in the docker group, but this terminal started before you were added."
      else
        echo "  Add yourself to the docker group: sudo usermod -aG docker \"\$USER\""
      fi
      if [ -n "${WSL_DISTRO_NAME:-}" ]; then
        echo "  Then run 'wsl --shutdown' in PowerShell, reopen this terminal and re-run 'labctl init'."
      else
        echo "  Then log out and back in (or run 'newgrp docker') and re-run 'labctl init'."
      fi
      return 1
      ;;
  esac
  return 0
}

# docker_cli_missing - succeed when no docker CLI is on PATH. A function so
# tests can stand in for a machine without one.
docker_cli_missing() {
  ! command -v docker >/dev/null 2>&1
}

# windows_docker_app prints the name of a Docker app installed on the Windows
# side of WSL (Docker Desktop or Rancher Desktop), and fails when there is none.
# While such an app is stopped, its docker CLI vanishes from the distro, so the
# lab must ask the user to start it rather than install a second engine that
# would fight it for /var/run/docker.sock. WINDOWS_PROGRAM_FILES is for tests.
windows_docker_app() {
  local pf="${WINDOWS_PROGRAM_FILES:-/mnt/c/Program Files}"
  if [ -e "$pf/Docker/Docker/Docker Desktop.exe" ]; then
    echo "Docker Desktop"
  elif [ -e "$pf/Rancher Desktop/Rancher Desktop.exe" ]; then
    echo "Rancher Desktop"
  else
    return 1
  fi
}

# ensure_docker_running - make the daemon answer, starting colima on macOS.
ensure_docker_running() {
  if docker info >/dev/null 2>&1; then
    return 0
  fi
  local problem
  if ! problem="$(docker_access_problem)"; then
    echo "ERROR: ${problem}" >&2
    return 1
  fi
  if [ "$(uname -s)" = "Darwin" ] && command -v colima >/dev/null 2>&1; then
    local cpus mem
    cpus="$(colima_size_arg cpus "$LAB_CPUS")"
    mem="$(colima_size_arg memory "$LAB_MEMORY")"
    echo "Starting colima with ${cpus} CPU / ${mem} GB (the first start downloads a VM image, ~1-2 min)..."
    colima start --cpu "$cpus" --memory "$mem" || return 1
    wait_for_docker 90 || return 1
    echo "Colima is running."
    return 0
  fi
  echo "ERROR: the Docker daemon is not running." >&2
  if [ "$(uname -s)" = "Darwin" ]; then
    echo "  Start Docker Desktop (open -a Docker), or install colima: brew install colima docker" >&2
  elif [ -n "${WSL_DISTRO_NAME:-}" ]; then
    local app
    if docker_cli_missing && app="$(windows_docker_app)"; then
      echo "  ${app} is installed on Windows but not running. Start it, make sure its" >&2
      echo "  WSL Integration is enabled for '${WSL_DISTRO_NAME}', then re-run 'labctl init'." >&2
    else
      echo "  Start Docker Desktop on Windows and enable Settings → Resources → WSL Integration" >&2
      echo "  for this distro, or start a native daemon: sudo service docker start" >&2
    fi
  else
    echo "  sudo systemctl start docker" >&2
  fi
  return 1
}

# pull_with_timeout <image> <seconds> - pull an image, giving up if it takes
# longer than seconds. A stalled registry download otherwise hangs cluster
# creation forever with no output. Returns 124 on timeout.
pull_with_timeout() {
  local image="$1" limit="$2" pid waited=0
  docker image inspect "$image" >/dev/null 2>&1 && return 0
  docker pull "$image" &
  pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$waited" -ge "$limit" ]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 5
    waited=$((waited + 5))
  done
  wait "$pid"
}

# prepull_images <image>... - pull each image with one retry, so a slow or
# stalled network surfaces as a clear error instead of a silent hang.
prepull_images() {
  local image rc
  for image in "$@"; do
    [ -n "$image" ] || continue
    rc=0
    pull_with_timeout "$image" 600 || rc=$?
    if [ "$rc" -ne 0 ]; then
      echo "Pull of ${image} failed or stalled - retrying once..." >&2
      rc=0
      pull_with_timeout "$image" 600 || rc=$?
    fi
    if [ "$rc" -ne 0 ]; then
      echo "ERROR: could not download ${image} (network stalled or registry unreachable)." >&2
      echo "  Check your connection (and any proxy/VPN), then re-run 'labctl init'." >&2
      return 1
    fi
  done
}

# cluster_record_file <cluster> - where labctl reads how this machine reaches
# the cluster's ingress (internal/config.ClusterStateFile).
cluster_record_file() {
  printf '%s/clusters/%s.env' "${SNOWOPS_HOME:-$HOME/.snowops}" "$1"
}

# record_value <cluster> <KEY> - print KEY from the cluster's record, if any.
record_value() {
  local file
  file="$(cluster_record_file "$1")"
  [ -f "$file" ] || return 0
  sed -n "s/^$2=//p" "$file" | tail -n 1
}

# write_record <cluster> <http-port> <https-port> <domain-suffix> - record the
# host ports and the domain suffix labctl builds every URL and check from.
write_record() {
  local file
  file="$(cluster_record_file "$1")"
  mkdir -p "$(dirname "$file")"
  printf 'HTTP_PORT=%s\nHTTPS_PORT=%s\nDOMAIN_SUFFIX=%s\n' "$2" "$3" "$4" >"$file"
}

# mapped_port <container> <container-port> <recorded> - the host port the
# container maps its port to: the recorded one while it is still mapped (a
# moved ingress maps several), else the first mapping.
mapped_port() {
  local ports
  ports="$(docker port "$1" "$2/tcp" 2>/dev/null | sed 's/.*://' | sort -u)"
  if [ -n "$3" ] && printf '%s\n' "$ports" | grep -qx "$3"; then
    printf '%s' "$3"
  else
    printf '%s\n' "$ports" | head -n 1
  fi
}

# ingress_suffix_in_use prints the domain suffix the current cluster's ingresses
# already use (from the Grafana or Prometheus host), so a lab built before its
# suffix was recorded keeps the hostnames it was built with.
ingress_suffix_in_use() {
  kubectl get ingress -A -o jsonpath='{range .items[*]}{.spec.rules[*].host}{"\n"}{end}' 2>/dev/null |
    sed -n -e 's/^grafana\.//p' -e 's/^prometheus\.//p' | head -n 1
}

# record_ingress_ports <cluster> <container> - record the host ports mapped to
# the container's :80 and :443 and the domain suffix of the cluster's ingress
# hostnames. A suffix already recorded is kept; the first record takes the one
# the cluster's ingresses use, else DOMAIN_SUFFIX.
record_ingress_ports() {
  local cluster="$1" container="$2" http https suffix
  http="$(mapped_port "$container" 80 "$(record_value "$cluster" HTTP_PORT)")"
  https="$(mapped_port "$container" 443 "$(record_value "$cluster" HTTPS_PORT)")"
  case "$http" in
    '' | *[!0-9]*) return 0 ;;
  esac
  suffix="$(record_value "$cluster" DOMAIN_SUFFIX)"
  [ -n "$suffix" ] || suffix="$(ingress_suffix_in_use)"
  [ -n "$suffix" ] || suffix="${DOMAIN_SUFFIX:-${cluster}.localhost}"
  write_record "$cluster" "$http" "${https:-443}" "$suffix"
}

# port_free <port> - 0 if nothing answers on the host TCP port. Uses bash's
# /dev/tcp so it needs no nc/lsof/ss, which differ across macOS and Linux. The
# subshell scopes fd 3.
port_free() {
  ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

# pick_port <preferred> <fallback-base> - echo <preferred> if it is free, else
# the first free port at or above <fallback-base>, so a second lab (or any
# service holding the port) does not stop this one from coming up.
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

# name_used_by <runtime> <cluster> - succeed when another runtime on this
# machine already has a cluster of that name. Names are unique per machine,
# because the lab's state and hostnames are keyed by them.
name_used_by() {
  case "$1" in
    k3d) command -v k3d >/dev/null 2>&1 && k3d cluster list "$2" >/dev/null 2>&1 ;;
    kind) command -v kind >/dev/null 2>&1 && kind get clusters 2>/dev/null | grep -qx "$2" ;;
    *) return 1 ;;
  esac
}

# node_memory_reserved <nodes> - the memory, in MiB, each of <nodes> cluster
# nodes holds back from its kubelet. The nodes are containers on one Docker
# engine and each kubelet reports all of the engine's memory as allocatable, so
# without a reservation the scheduler fills every node as if it had the whole
# VM. Holding back all but an even share makes the nodes' allocatable memory
# add up to what the engine has. Prints nothing for a single node or when the
# engine's memory cannot be read.
node_memory_reserved() {
  local nodes="$1" total
  total="$(docker info --format '{{.MemTotal}}' 2>/dev/null || true)"
  case "$total" in
    '' | *[!0-9]*) return 0 ;;
  esac
  [ "$nodes" -gt 1 ] || return 0
  total=$((total / 1048576))
  printf '%s' $((total - total / nodes))
}

# forget_cluster <cluster> - drop the record when the cluster is deleted, so a
# new cluster starts from the configured ports again.
forget_cluster() {
  rm -f "$(cluster_record_file "$1")"
}
