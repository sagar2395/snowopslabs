#!/usr/bin/env bash
set -euo pipefail

# Grades the promotion pipeline invariant: for every environment, the release
# record (env-metadata.declared_tag) must match what is ACTUALLY running.
#
# "Actually running" is checked at three levels, because each catches a
# different real-world failure:
#   1. Deployment .spec  — what you asked for
#   2. Rollout status    — whether the ask converged (a wedged rollout leaves
#                          .spec at vNEXT while the pods still serve vPREV)
#   3. Pod .status image — what the kubelet reports it is really running
#
# This is the same invariant GitOps tools call drift: desired vs live state.

ENVS="dev staging prod"
APP="go-api"
REPO="go-api"
fail=0

jp() { kubectl get "$1" "$2" -n "$3" -o jsonpath="$4" 2>/dev/null || echo ""; }

for env_name in $ENVS; do
  ns="env-${env_name}"

  declared=$(kubectl get cm env-metadata -n "$ns" -o jsonpath='{.data.declared_tag}' 2>/dev/null || echo "")
  spec_image=$(jp deployment "$APP" "$ns" '{.spec.template.spec.containers[0].image}')

  if [ -z "$declared" ] || [ -z "$spec_image" ]; then
    echo "FAIL: ${ns}: missing deployment or env-metadata (run 'labctl scenario up env-promotion')." >&2
    fail=1
    continue
  fi

  # Compare the FULL image reference, not just the tag: "otherrepo/go-api:v1.1.0"
  # must not satisfy declared_tag=v1.1.0.
  want="${REPO}:${declared}"

  if [ "$spec_image" != "$want" ]; then
    echo "FAIL: ${env_name}: declared_tag=${declared} (expects ${want}) but the Deployment spec is ${spec_image}." >&2
    echo "      Roll out the declared tag AND record it together:" >&2
    echo "        kubectl -n ${ns} set image deployment/${APP} ${APP}=${want}" >&2
    echo "        kubectl -n ${ns} rollout status deployment/${APP}" >&2
    fail=1
    continue
  fi

  # A rollout that never converged still shows the new image in .spec. Compare
  # the generation the controller has observed, and the replica counts.
  generation=$(jp deployment "$APP" "$ns" '{.metadata.generation}')
  observed=$(jp deployment "$APP" "$ns" '{.status.observedGeneration}')
  desired=$(jp deployment "$APP" "$ns" '{.spec.replicas}')
  updated=$(jp deployment "$APP" "$ns" '{.status.updatedReplicas}')
  ready=$(jp deployment "$APP" "$ns" '{.status.readyReplicas}')

  if [ "$generation" != "$observed" ] || [ "${updated:-0}" != "$desired" ] || [ "${ready:-0}" != "$desired" ]; then
    echo "FAIL: ${env_name}: the rollout to ${want} has not converged." >&2
    echo "      generation=${generation} observed=${observed} desired=${desired} updated=${updated:-0} ready=${ready:-0}" >&2
    echo "      Find out why:" >&2
    echo "        kubectl -n ${ns} rollout status deployment/${APP}" >&2
    echo "        kubectl -n ${ns} get pods -o wide" >&2
    fail=1
    continue
  fi

  # What each RUNNING pod was created with. This catches the case the Deployment
  # spec cannot: pods still alive from an older ReplicaSet carry the previous
  # image in their own spec.
  #
  # Deliberately .spec, not .status.containerStatuses[].image: when several tags
  # share one digest in the node's image store (the lab builds tag both vX.Y.Z
  # and :latest), the kubelet reports whichever tag it resolved — commonly
  # "docker.io/library/go-api:latest" for a pod that correctly requested
  # go-api:v1.1.0. Comparing tags against that field reports drift that is not
  # there.
  running_images=$(kubectl get pods -n "$ns" -l app="$APP" \
    --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.spec.containers[0].image}{"\n"}{end}' 2>/dev/null | sort -u)

  if [ -z "$running_images" ]; then
    echo "FAIL: ${env_name}: no ${APP} pods found in ${ns}." >&2
    fail=1
    continue
  fi

  mismatch=0
  for img in $running_images; do
    # Tolerate a registry/library prefix on an otherwise identical reference.
    case "$img" in
      "$want" | */"$want") ;;
      *) mismatch=1 ;;
    esac
  done

  if [ "$mismatch" = "1" ]; then
    echo "FAIL: ${env_name}: pods from an older rollout are still running $(echo "$running_images" | tr '\n' ' ')while declared_tag is ${declared}." >&2
    echo "      Look at the ReplicaSets: kubectl -n ${ns} get rs -o wide" >&2
    fail=1
    continue
  fi

  echo "OK:   ${env_name}: declared=${declared}  spec=${spec_image}  rollout=converged  pods=${want}"
done

if [ "$fail" = "0" ]; then
  echo ""
  echo "All environments consistent. Confirm the running binary agrees:"
  echo "  for e in dev staging prod; do echo -n \"\$e: \"; curl -s go-api-\$e.\${DOMAIN_SUFFIX:-k3d.local}/version; echo; done"
fi

exit "$fail"
