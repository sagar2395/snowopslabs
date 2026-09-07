#!/usr/bin/env bash
set -euo pipefail

# roll-node.sh — replace ONE already-drained worker node with a fresh one on the
# target k3s version.
#
# This script deliberately does NOT cordon or drain. Those are the transferable
# skills of the drill and the learner performs them with kubectl; the script
# refuses to run until they are done. What it does own is the part kubectl
# cannot express on k3d: a node "upgrade" here is delete + recreate on a new
# image, standing in for the node-group roll a managed provider performs.
#
# Usage:
#   TARGET_K3S_VERSION=v1.33.6-k3s1 bash roll-node.sh k3d-snowops-agent-0
#
# Env:
#   CLUSTER_NAME        k3d cluster name (default: snowops)
#   TARGET_K3S_VERSION  k3s image tag for the replacement (REQUIRED)
#   READY_TIMEOUT       seconds to wait for the new node to go Ready (default: 240)

CLUSTER_NAME="${CLUSTER_NAME:-snowops}"
READY_TIMEOUT="${READY_TIMEOUT:-240}"
NODE="${1:-}"

die() {
  echo "ERROR: $*" >&2
  exit 1
}

[ -n "$NODE" ] || die "usage: TARGET_K3S_VERSION=<tag> bash roll-node.sh <node-name>"
[ -n "${TARGET_K3S_VERSION:-}" ] ||
  die "TARGET_K3S_VERSION is required (e.g. v1.33.6-k3s1). Current versions: kubectl get nodes"

for bin in k3d kubectl docker; do
  command -v "$bin" >/dev/null 2>&1 || die "'$bin' is required but not installed."
done

kubectl get node "$NODE" >/dev/null 2>&1 || die "node '${NODE}' not found. See: kubectl get nodes"

# --- Guard against a downgrade or an unsupported version skew ----------------
# The kubelet may trail the API server by at most three minor versions, and a
# node must never come back older than the cluster it rejoins. Getting this
# wrong is the classic real-world upgrade outage, so it is a hard stop.
server_ver="$(kubectl get node -l node-role.kubernetes.io/control-plane \
  -o jsonpath='{.items[0].status.nodeInfo.kubeletVersion}' 2>/dev/null || true)"
minor_of() { echo "$1" | sed 's/^v//' | cut -d. -f2 | sed 's/[^0-9].*//'; }
target_minor="$(minor_of "$TARGET_K3S_VERSION")"
server_minor="$(minor_of "${server_ver:-v0.0.0}")"
node_ver="$(kubectl get node "$NODE" -o jsonpath='{.status.nodeInfo.kubeletVersion}')"
node_minor="$(minor_of "$node_ver")"

if [ -n "$target_minor" ] && [ -n "$node_minor" ] && [ "$target_minor" -lt "$node_minor" ]; then
  die "refusing to downgrade ${NODE}: it runs ${node_ver}, target is ${TARGET_K3S_VERSION}."
fi
if [ -n "$target_minor" ] && [ -n "$server_minor" ] && [ "$((server_minor - target_minor))" -gt 3 ]; then
  die "kubelet ${TARGET_K3S_VERSION} would trail the ${server_ver} control plane by more than three minor versions."
fi
if [ -n "$target_minor" ] && [ -n "$server_minor" ] && [ "$target_minor" -gt "$server_minor" ]; then
  echo "WARN: target ${TARGET_K3S_VERSION} is newer than the ${server_ver} control plane." >&2
  echo "      Real clusters upgrade the control plane FIRST. k3d cannot, so this" >&2
  echo "      drill rolls workers only — see the scenario's honest-scope tip." >&2
fi

# --- Refuse to run until the learner has cordoned and drained ----------------
schedulable="$(kubectl get node "$NODE" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || true)"
if [ "$schedulable" != "true" ]; then
  die "${NODE} is not cordoned. Drain it first:
       kubectl cordon ${NODE}
       kubectl drain ${NODE} --ignore-daemonsets --delete-emptydir-data --timeout=120s"
fi

remaining="$(kubectl get pods --all-namespaces --field-selector "spec.nodeName=${NODE}" \
  -o jsonpath='{range .items[*]}{.metadata.ownerReferences[0].kind}{" "}{.metadata.namespace}{"/"}{.metadata.name}{"\n"}{end}' \
  2>/dev/null | grep -v '^DaemonSet ' || true)"
if [ -n "$remaining" ]; then
  echo "ERROR: ${NODE} still hosts non-DaemonSet pods — it is not drained:" >&2
  echo "$remaining" >&2
  die "finish the drain first: kubectl drain ${NODE} --ignore-daemonsets --delete-emptydir-data"
fi

# --- Warn about local storage that will not survive the replacement ----------
# local-path PVs carry node affinity. Replacing the node strands the claim
# Pending forever. This is a real consequence of node replacement, not a k3d
# quirk, so it is surfaced rather than silently accepted.
stranded="$(kubectl get pv -o jsonpath="{range .items[?(@.spec.nodeAffinity)]}{.metadata.name}{' -> '}{.spec.claimRef.namespace}{'/'}{.spec.claimRef.name}{' @'}{.spec.nodeAffinity.required.nodeSelectorTerms[0].matchExpressions[0].values[0]}{'\n'}{end}" 2>/dev/null |
  grep "@${NODE}\$" || true)"
if [ -n "$stranded" ]; then
  echo ""
  echo "WARNING: these PersistentVolumes are pinned to ${NODE} and will be lost:" >&2
  echo "$stranded" >&2
  echo "         Their claims will sit Pending until you delete and recreate them." >&2
  echo "         On a real cluster this is why stateful workloads need replicated" >&2
  echo "         storage before you roll a node. Continuing in 5s (Ctrl-C to abort)." >&2
  sleep 5
fi

before="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | sort)"

echo "==> Removing ${NODE} (${node_ver}) from the cluster"
k3d node delete "$NODE" >/dev/null
kubectl delete node "$NODE" --ignore-not-found >/dev/null 2>&1 || true

# k3d prefixes the name it is given with "k3d-" and appends its own index, so
# the final node name is not predictable from the argument. Strip both so that
# rolling the same node twice reuses the name instead of growing a new suffix
# each time, then diff the node list to learn what k3d actually called it.
base="$(echo "$NODE" | sed "s/^k3d-//" | sed -E "s/-([0-9]+)-[0-9]+$/-\1/")"
echo "==> Adding a replacement agent on rancher/k3s:${TARGET_K3S_VERSION}"
k3d node create "${base}" \
  --cluster "$CLUSTER_NAME" \
  --role agent \
  --image "rancher/k3s:${TARGET_K3S_VERSION}" \
  --wait >/dev/null 2>&1 || true

# The colima/Docker VM defaults fs.inotify.max_user_instances to 128, which is
# too low for containerd's CNI watcher: the CRI plugin fails to load with "too
# many open files" and the node never reports Ready. runtimes/k3d/up.sh raises
# this at cluster-create time, so a node created later must raise it too.
echo "==> Raising inotify limits on the cluster's nodes"
for n in $(docker ps --filter "label=k3d.cluster=${CLUSTER_NAME}" --format '{{.Names}}' 2>/dev/null); do
  docker exec "$n" sysctl -w fs.inotify.max_user_instances=512 >/dev/null 2>&1 || true
done

after="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | sort)"
NEW_NODE="$(comm -13 <(echo "$before") <(echo "$after") | head -1)"
[ -n "$NEW_NODE" ] || die "the replacement node did not register with the API server."

# containerd may already have failed its CNI watcher before the sysctl landed;
# a restart makes the new limit take effect.
if ! kubectl wait --for=condition=Ready "node/${NEW_NODE}" --timeout=60s >/dev/null 2>&1; then
  echo "==> ${NEW_NODE} is not Ready yet; restarting it so the new inotify limit applies"
  docker restart "$NEW_NODE" >/dev/null 2>&1 || true
fi

echo "==> Waiting for ${NEW_NODE} to become Ready (up to ${READY_TIMEOUT}s)"
if ! kubectl wait --for=condition=Ready "node/${NEW_NODE}" --timeout="${READY_TIMEOUT}s" >/dev/null 2>&1; then
  echo "ERROR: ${NEW_NODE} did not become Ready." >&2
  echo "       Check containerd on the node:" >&2
  echo "         docker exec ${NEW_NODE} tail -20 /var/lib/rancher/k3s/agent/containerd/containerd.log" >&2
  exit 1
fi

# A fresh node has an empty image store. Locally built lab images (go-api,
# echo-server) were side-loaded into the cluster at deploy time and are in no
# registry, so without re-importing them the app cannot start on the
# replacement and lands in ImagePullBackOff.
echo "==> Re-importing locally built images onto the new node"
LOCAL_IMAGES="$(kubectl get pods --all-namespaces \
  -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{end}' 2>/dev/null |
  sort -u | grep -v '/' || true)"
for img in $LOCAL_IMAGES; do
  if docker image inspect "$img" >/dev/null 2>&1; then
    echo "    importing ${img}"
    k3d image import "$img" -c "$CLUSTER_NAME" >/dev/null 2>&1 ||
      echo "    WARN: could not import ${img}" >&2
  fi
done

echo ""
echo "${NODE} (${node_ver}) replaced by ${NEW_NODE} (${TARGET_K3S_VERSION})."
echo "Next: let the workload reschedule, then roll the remaining node."
echo "  kubectl get nodes -o wide"
echo "  kubectl -n go-api get pods -o wide"
