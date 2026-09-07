#!/usr/bin/env bash
set -euo pipefail

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

echo "OK: every node is on ${DISTINCT} — no node left behind."
