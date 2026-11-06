#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Keeps k3d's load balancer pointed at nodes that exist.
#
# Sourced, not executed:
#   . "$PROJECT_ROOT/runtimes/_lib/k3d-serverlb.sh"
#
# k3d-<cluster>-serverlb holds its nginx upstreams as a list of node names in
# /etc/confd/values.yaml. k3d rewrites that list when a server joins or leaves,
# never for an agent, so replacing or removing an agent leaves its name behind.
# Nothing shows while nginx keeps running; on the next restart nginx fails with
# `host not found in upstream`, the load balancer restart-loops, and the API
# server behind it (port 6443) is unreachable too.

# serverlb_repair <cluster> replaces every upstream that names a container that
# no longer exists with the cluster's nodes of the same role that the port does
# not list yet (all servers when a port would be left empty), then restarts the
# load balancer. It prints one line per name it replaced, so it prints nothing
# and restarts nothing for a cluster without a load balancer or a config that
# names only live nodes. Returns 1 when the config could not be read or written.
serverlb_repair() {
  local cluster="$1" lb="k3d-$1-serverlb" dir missing added
  docker inspect "$lb" >/dev/null 2>&1 || return 0
  dir="$(mktemp -d)"
  # -a: a node that is only stopped still resolves once it starts again.
  docker ps -a --filter "label=k3d.cluster=${cluster}" \
    --format '{{.Names}} {{.Label "k3d.role"}}' >"$dir/nodes" 2>/dev/null || true
  # Without the node list every upstream would look stale.
  if ! grep -q ' server$' "$dir/nodes" ||
    ! docker cp "${lb}:/etc/confd/values.yaml" "$dir/values.yaml" >/dev/null 2>&1; then
    rm -rf "$dir"
    return 1
  fi
  : >"$dir/changes"
  awk -v changes="$dir/changes" '
    function item_name(line) {
      sub(/^[ ]*- /, "", line)
      gsub(/["\r]/, "", line)
      sub(/[ ]+$/, "", line)
      return line
    }
    FNR == 1 { pass++ }
    pass == 1 {
      role[$1] = $2
      if ($2 == "server" || $2 == "agent") order[++n] = $1
      next
    }
    {
      is_port = ($0 ~ /^[ ]*[0-9]+\.(tcp|udp):[ ]*$/)
      is_item = ($0 ~ /^[ ]*- /)
      if (is_port) port = $0
      else if (!is_item) port = ""
    }
    pass == 2 {
      if (is_item && port != "") {
        name = item_name($0)
        listed[port SUBSEP name] = 1
        if (name in role) alive[port]++
      }
      next
    }
    !is_item || port == "" { print; next }
    {
      name = item_name($0)
      if (name in role) { print; next }
      indent = substr($0, 1, index($0, "-") + 1)
      want = (name ~ /-server-[0-9]+$/) ? "server" : "agent"
      added = ""
      for (i = 1; i <= n; i++) {
        c = order[i]
        if (role[c] == want && !((port SUBSEP c) in listed)) {
          print indent c
          listed[port SUBSEP c] = 1
          added = added " " c
        }
      }
      if (added == "" && !alive[port]) {
        for (i = 1; i <= n; i++) {
          c = order[i]
          if (role[c] == "server" && !((port SUBSEP c) in listed)) {
            print indent c
            listed[port SUBSEP c] = 1
            added = added " " c
          }
        }
      }
      if (added != "") alive[port]++
      print name "\t" substr(added, 2) >changes
    }
  ' "$dir/nodes" "$dir/values.yaml" "$dir/values.yaml" >"$dir/values.new"
  if cmp -s "$dir/values.yaml" "$dir/values.new"; then
    rm -rf "$dir"
    return 0
  fi
  sort -u "$dir/changes" | while IFS="$(printf '\t')" read -r missing added; do
    if [ -n "$added" ]; then
      echo "    ${missing} no longer exists; the load balancer now uses ${added}"
    else
      echo "    ${missing} no longer exists; removed it from the load balancer"
    fi
  done
  if ! docker cp "$dir/values.new" "${lb}:/etc/confd/values.yaml" >/dev/null 2>&1 ||
    ! docker restart "$lb" >/dev/null 2>&1; then
    rm -rf "$dir"
    return 1
  fi
  rm -rf "$dir"
}
