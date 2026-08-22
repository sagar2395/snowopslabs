#!/usr/bin/env bash
set -euo pipefail

# list.sh — print a table of environments and their current declared image tags.
# Called by 'labctl env list'.

ENVS="dev staging prod"

printf "%-10s %-12s %-12s %-10s %-25s\n" "ENV" "APP" "TAG" "STATUS" "PROMOTED_AT"
printf "%-10s %-12s %-12s %-10s %-25s\n" "---" "---" "---" "------" "-----------"

for env_name in $ENVS; do
  ns="env-${env_name}"

  if ! kubectl get namespace "$ns" &>/dev/null; then
    printf "%-10s %-12s %-12s %-10s\n" "$env_name" "-" "-" "no-namespace"
    continue
  fi

  tag=$(kubectl get cm env-metadata -n "$ns" \
    -o jsonpath='{.data.image_tag}' 2>/dev/null || echo "n/a")
  app=$(kubectl get cm env-metadata -n "$ns" \
    -o jsonpath='{.data.app}' 2>/dev/null || echo "go-api")
  promoted_at=$(kubectl get cm env-metadata -n "$ns" \
    -o jsonpath='{.data.promoted_at}' 2>/dev/null || echo "")

  ready=$(kubectl get deploy "$app" -n "$ns" \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
  desired=$(kubectl get deploy "$app" -n "$ns" \
    -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")

  if [ "${ready:-0}" = "${desired:-0}" ] && [ "${desired:-0}" != "0" ]; then
    status="running"
  else
    status="not-ready"
  fi

  printf "%-10s %-12s %-12s %-10s %-25s\n" \
    "$env_name" "$app" "$tag" "$status" "${promoted_at:-never}"
done
