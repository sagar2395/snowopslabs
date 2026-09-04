#!/usr/bin/env bash
# Shared helpers for platform install scripts. Source it:
#   . "$(cd "$(dirname "$0")/../.." && pwd)/_lib/helm.sh"

# helm_upgrade_install <release> <namespace> [helm args...]
#
# `helm upgrade --install` that survives a chart upgrade changing an immutable
# StatefulSet field. Most of a StatefulSet's spec (volumeClaimTemplates,
# serviceName, selector) cannot be changed in place, so a chart major bump fails
# with:
#
#   Forbidden: updates to statefulset spec for fields other than 'replicas',
#   'ordinals', 'template', ... are forbidden
#
# Deleting the StatefulSet with --cascade=orphan removes only the controller —
# the pods and their PVCs keep running and are re-adopted by the new spec, so no
# data is lost. Any other failure is returned unchanged.
helm_upgrade_install() {
  release="$1"
  namespace="$2"
  shift 2

  out_file="$(mktemp "${TMPDIR:-/tmp}/helm-out.XXXXXX")"
  if helm upgrade --install "$release" "$@" 2>&1 | tee "$out_file"; then
    rm -f "$out_file"
    return 0
  fi

  if ! grep -q "updates to statefulset spec for fields other than" "$out_file"; then
    rm -f "$out_file"
    return 1
  fi

  # Pull the StatefulSet name out of either error shape helm reports.
  sts="$(sed -n 's/.*object [^ /]*\/\([^ ]*\) apps\/v1, Kind=StatefulSet.*/\1/p' "$out_file" | head -1)"
  if [ -z "$sts" ]; then
    sts="$(sed -n 's/.*cannot patch "\([^"]*\)" with kind StatefulSet.*/\1/p' "$out_file" | head -1)"
  fi
  rm -f "$out_file"

  if [ -z "$sts" ]; then
    echo "Could not determine which StatefulSet blocked the upgrade." >&2
    return 1
  fi

  echo ""
  echo "StatefulSet '${sts}' has immutable fields that differ from the new chart."
  echo "Deleting the controller with --cascade=orphan (pods and PVCs stay up) and retrying..."
  kubectl delete statefulset "$sts" --namespace "$namespace" --cascade=orphan --ignore-not-found

  helm upgrade --install "$release" "$@"
}
