#!/usr/bin/env bash
set -euo pipefail

# promote.sh <from-env> <to-env> [--app <app>]
#
# Promotes the declared image tag for an app from one environment to another.
# "Promotion" in namespace mode means:
#   1. Read the declared tag from the source env's env-metadata ConfigMap
#   2. Update the destination env-metadata ConfigMap with that tag
#   3. Patch the destination Deployment label to record the declared tag
#
# In a full GitOps pipeline this step would commit updated values to a Git
# repo that ArgoCD reconciles. In namespace mode the ConfigMap is the
# stand-in for the GitOps manifest store — same concept, no extra infra.

FROM_ENV="${1:?Usage: promote.sh <from-env> <to-env> [<app>]}"
TO_ENV="${2:?Usage: promote.sh <from-env> <to-env> [<app>]}"
APP="${3:-go-api}"

FROM_NS="env-${FROM_ENV}"
TO_NS="env-${TO_ENV}"

valid_env() {
  case "$1" in dev | staging | prod) return 0 ;; *) return 1 ;; esac
}

if ! valid_env "$FROM_ENV"; then
  echo "ERROR: unknown environment '${FROM_ENV}'. Valid values: dev, staging, prod" >&2
  exit 1
fi
if ! valid_env "$TO_ENV"; then
  echo "ERROR: unknown environment '${TO_ENV}'. Valid values: dev, staging, prod" >&2
  exit 1
fi
if [ "$FROM_ENV" = "$TO_ENV" ]; then
  echo "ERROR: source and destination environments must be different" >&2
  exit 1
fi

# Read the source declared tag
SRC_TAG=$(kubectl get cm env-metadata -n "$FROM_NS" \
  -o jsonpath='{.data.image_tag}' 2>/dev/null || true)
if [ -z "$SRC_TAG" ]; then
  echo "ERROR: could not read image_tag from ${FROM_NS}/env-metadata" >&2
  echo "       Activate the scenario first: labctl scenario up env-promotion" >&2
  exit 1
fi

# Read the destination's current tag (for display only)
DST_TAG=$(kubectl get cm env-metadata -n "$TO_NS" \
  -o jsonpath='{.data.image_tag}' 2>/dev/null || echo "(none)")

if [ "$SRC_TAG" = "$DST_TAG" ]; then
  echo "${TO_ENV} is already on ${SRC_TAG} — nothing to promote."
  exit 0
fi

PROMOTED_AT=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

echo "Promoting ${APP}: ${FROM_ENV} (${SRC_TAG}) → ${TO_ENV} (${DST_TAG} → ${SRC_TAG})"

# Update destination env-metadata ConfigMap
kubectl patch cm env-metadata -n "$TO_NS" \
  --type=merge \
  -p "{\"data\":{\"image_tag\":\"${SRC_TAG}\",\"promoted_from\":\"${FROM_ENV}\",\"promoted_at\":\"${PROMOTED_AT}\"}}"

# Patch the deployment label so 'kubectl get deploy' shows the declared tag
kubectl patch deployment "$APP" -n "$TO_NS" \
  --type=merge \
  -p "{\"metadata\":{\"labels\":{\"snowops.net/image-tag\":\"${SRC_TAG}\"}}}" \
  2>/dev/null || true

echo ""
echo "  ${FROM_ENV}: ${SRC_TAG}  (unchanged)"
echo "  ${TO_ENV}:  ${SRC_TAG}  (was ${DST_TAG})"
echo ""
echo "Run 'labctl env list' to confirm, or 'labctl env promote ${TO_ENV} prod' to continue the pipeline."
