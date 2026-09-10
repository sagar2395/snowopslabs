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

# Identify nodes by UID, not by name. k3d reuses the name whenever it can — a
# node already called agent-1-0 comes back as agent-1-0 — so a name diff finds
# nothing and concludes the replacement never registered, when in fact it
# registered perfectly. A recreated node always has a new UID.
before="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.uid}{"\n"}{end}' | sort)"

echo "==> Removing ${NODE} (${node_ver}) from the cluster"
k3d node delete "$NODE" >/dev/null
kubectl delete node "$NODE" --ignore-not-found >/dev/null 2>&1 || true

# k3d prefixes the name it is given with "k3d-" and appends its own index, so
# the final node name is not predictable from the argument. Strip both so that
# rolling the same node twice reuses the name instead of growing a new suffix
# each time, then diff the node list to learn what k3d actually called it.
base="$(echo "$NODE" | sed "s/^k3d-//" | sed -E "s/-([0-9]+)-[0-9]+$/-\1/")"
echo "==> Adding a replacement agent on rancher/k3s:${TARGET_K3S_VERSION}"
create_log="$(mktemp)"
if ! k3d node create "${base}" \
  --cluster "$CLUSTER_NAME" \
  --role agent \
  --image "rancher/k3s:${TARGET_K3S_VERSION}" \
  --wait >"$create_log" 2>&1; then
  # Do not swallow this. The failure that follows — "the replacement node did
  # not register" — describes a symptom, and the cause is in here.
  echo "ERROR: k3d could not create the replacement node:" >&2
  tail -5 "$create_log" >&2
  rm -f "$create_log"
  exit 1
fi
rm -f "$create_log"

# The colima/Docker VM defaults fs.inotify.max_user_instances to 128, which is
# too low for containerd's CNI watcher: the CRI plugin fails to load with "too
# many open files" and the node never reports Ready. runtimes/k3d/up.sh raises
# this at cluster-create time, so a node created later must raise it too.
echo "==> Raising inotify limits on the cluster's nodes"
for n in $(docker ps --filter "label=k3d.cluster=${CLUSTER_NAME}" --format '{{.Names}}' 2>/dev/null); do
  docker exec "$n" sysctl -w fs.inotify.max_user_instances=512 >/dev/null 2>&1 || true
done

# Registration lags container creation by several seconds, so poll rather than
# read once — the single read raced the kubelet and reported a failure that had
# not happened.
NEW_NODE=""
for _ in $(seq 1 30); do
  NEW_UID="$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.uid}{"\n"}{end}' 2>/dev/null |
    sort | comm -13 <(echo "$before") - | head -1)"
  if [ -n "$NEW_UID" ]; then
    NEW_NODE="$(kubectl get nodes -o jsonpath="{range .items[?(@.metadata.uid=='${NEW_UID}')]}{.metadata.name}{end}" 2>/dev/null || true)"
    [ -n "$NEW_NODE" ] && break
  fi
  sleep 2
done
[ -n "$NEW_NODE" ] || die "the replacement node did not register with the API server within 60s."

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

# A fresh node has an empty image store. Locally built lab images (${WORKLOAD_NAME:-go-api},
# ${WORKLOAD_NAME:-go-api}) were side-loaded into the cluster at deploy time and are in no
# registry, so without re-importing them the app cannot start on the
# replacement and lands in ImagePullBackOff.
echo "==> Re-importing locally built images onto the new node"
LOCAL_IMAGES="$(kubectl get pods --all-namespaces \
  -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{end}' 2>/dev/null |
  sort -u | grep -v '/' || true)"
MISSING=""
for img in $LOCAL_IMAGES; do
  if docker image inspect "$img" >/dev/null 2>&1; then
    echo "    importing ${img}"
    k3d image import "$img" -c "$CLUSTER_NAME" >/dev/null 2>&1 ||
      echo "    WARN: could not import ${img}" >&2
  else
    MISSING="${MISSING}${img} "
  fi
done

# Silence here is expensive: an image a running pod needs, that is not in the
# local daemon, cannot be imported and is in no registry either — so the pod
# lands in ImagePullBackOff on the replacement node and the cause is three steps
# back. Say so now, while the node roll is still on screen.
if [ -n "${MISSING// /}" ]; then
  echo ""
  echo "WARNING: these images are in use but are no longer in the local Docker daemon," >&2
  echo "         so they could not be imported onto ${NEW_NODE}:" >&2
  printf '           %s\n' ${MISSING} >&2
  echo "         Any pod that needs one will land in ImagePullBackOff — they are lab" >&2
  echo "         builds and exist in no registry. Rebuild before rescheduling, e.g.:" >&2
  echo "           DOCKER_IMAGE_TAG=<tag> bash src/engine/build/docker.sh <app> --import" >&2
fi

# k3d's load balancer holds its nginx upstreams as a fixed list of node names,
# captured when the cluster was created. Replacing a node changes that name, and
# nothing updates the list — so the LB keeps pointing at a container that no
# longer exists.
#
# It is invisible while the cluster keeps running, because nginx is already up.
# On the next restart confd re-renders the config, nginx fails validation on the
# missing host, and the load balancer never starts:
#
#   [emerg] host not found in upstream "k3d-snowops-agent-0:443"
#
# One dead upstream takes down the WHOLE listener set, including the 6443 mapping
# the kubeconfig points at — so the entire lab, API server included, is
# unreachable until someone repairs it by hand.
LB="k3d-${CLUSTER_NAME}-serverlb"
if docker inspect "$LB" >/dev/null 2>&1; then
  echo "==> Repointing the cluster load balancer from ${NODE} to ${NEW_NODE}"
  LB_VALUES="$(mktemp)"
  if docker cp "${LB}:/etc/confd/values.yaml" "$LB_VALUES" >/dev/null 2>&1; then
    # Whole-line match: node names are prefixes of one another
    # (agent-0 vs agent-0-0), so a substring replace corrupts the list.
    sed "s|^\( *- \)${NODE}\$|\1${NEW_NODE}|" "$LB_VALUES" >"${LB_VALUES}.new"
    if ! cmp -s "$LB_VALUES" "${LB_VALUES}.new"; then
      docker cp "${LB_VALUES}.new" "${LB}:/etc/confd/values.yaml" >/dev/null 2>&1 &&
        docker restart "$LB" >/dev/null 2>&1 &&
        echo "    load balancer updated and restarted"
    else
      # k3d reuses the name where it can, in which case the existing upstream
      # entry is already correct and there is nothing to rewrite.
      echo "    upstream list already names ${NEW_NODE}; nothing to change"
    fi
    rm -f "$LB_VALUES" "${LB_VALUES}.new"
  else
    echo "    WARN: could not read the load balancer's config; if the lab becomes" >&2
    echo "          unreachable after a restart, see docs/scenarios.md." >&2
  fi
fi

echo ""
echo "${NODE} (${node_ver}) replaced by ${NEW_NODE} (${TARGET_K3S_VERSION})."
echo "Next: let the workload reschedule, then roll the remaining node."
echo "  kubectl get nodes -o wide"
echo "  kubectl -n ${WORKLOAD_NAMESPACE:-go-api} get pods -o wide"
