#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=checks/_drill.sh
. "${SCRIPT_DIR}/_drill.sh"

# A cluster nobody has touched passes every version test there is: one distinct
# version across every node is what "finished" and "never started" both look
# like. So ask the checkpoint which workers were here when the drill began, and
# refuse to call it finished while any of them is still running.
RECORDED="$(checkpoint workers)"
if [ -z "$RECORDED" ]; then
  echo "NOT COMPLETE: no checkpoint from activation, so a rolled cluster cannot be" >&2
  echo "  told from an untouched one. Re-activate: labctl scenario up cluster-upgrade-drill --force" >&2
  exit 1
fi

REMAINING="$(unrolled_workers)"
if [ -n "${REMAINING// /}" ]; then
  echo "PENDING: these worker node(s) are the ones the drill started with and have not been rolled:" >&2
  printf '  %s\n' ${REMAINING} >&2
  echo "  Roll each of them, one at a time — cordon, drain, then replace:" >&2
  echo "    kubectl cordon <node> && kubectl drain <node> --ignore-daemonsets --delete-emptydir-data" >&2
  echo "    TARGET_K3S_VERSION=<tag> bash scenarios/cluster-upgrade-drill/scripts/roll-node.sh <node>" >&2
  exit 1
fi

mark_roll_started >/dev/null

VERSIONS="$(kubectl get nodes \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.nodeInfo.kubeletVersion}{"\n"}{end}' \
  2>/dev/null || true)"

if [ -z "$VERSIONS" ]; then
  echo "FAIL: could not read node versions from the cluster." >&2
  exit 1
fi

DISTINCT="$(echo "$VERSIONS" | awk '{print $2}' | sort -u)"
COUNT="$(echo "$DISTINCT" | grep -c . || true)"

if [ "${COUNT:-0}" -gt 1 ]; then
  echo "FAIL: the cluster is running mixed kubelet versions — the roll is unfinished:" >&2
  echo "$VERSIONS" >&2
  exit 1
fi

if [ -n "${TARGET_K3S_VERSION:-}" ]; then
  # kubeletVersion renders as v1.33.6+k3s1 where the image tag is v1.33.6-k3s1.
  WANT="$(echo "$TARGET_K3S_VERSION" | tr '-' '+')"
  if [ "$DISTINCT" != "$WANT" ]; then
    echo "FAIL: nodes report ${DISTINCT}, but TARGET_K3S_VERSION asks for ${WANT}." >&2
    exit 1
  fi
  echo "OK: every node is on ${DISTINCT} (matches TARGET_K3S_VERSION)."
  exit 0
fi

echo "OK: every worker the drill started with has been replaced, and every node is"
echo "now on ${DISTINCT} — no node left behind."
