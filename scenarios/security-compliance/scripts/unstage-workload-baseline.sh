#!/usr/bin/env bash
# Put the shared workload back, and take the learner's own objects with it.
#
# Two things outlive a plain manifest teardown here: the securityContext the
# learner patched onto the shared Deployment, and the Certificate and Ingress TLS
# they applied from the snippets — snippets belong to no component, so nothing
# has ever removed them.
set -euo pipefail
. "$(dirname "$0")/../../_lib/workload.sh"

NS="${WORKLOAD_NAMESPACE}"
WORKLOAD="${WORKLOAD_NAME}"
STATE="security-baseline"

BEFORE="$(kubectl -n "$NS" get configmap "$STATE" \
  -o jsonpath='{.data.security-context}' 2>/dev/null || true)"

if [ -n "$BEFORE" ] && kubectl -n "$NS" get deploy "$WORKLOAD" >/dev/null 2>&1; then
  NOW="$(kubectl -n "$NS" get deploy "$WORKLOAD" \
    -o jsonpath='{.spec.template.spec.containers[0].securityContext}' 2>/dev/null || true)"
  if [ "$NOW" != "$BEFORE" ]; then
    echo "Restoring ${WORKLOAD}'s securityContext to what the scenario found."
    if [ "$BEFORE" = "null" ]; then
      kubectl -n "$NS" patch deploy "$WORKLOAD" --type=json \
        -p '[{"op":"remove","path":"/spec/template/spec/containers/0/securityContext"}]' >/dev/null 2>&1 || true
    else
      kubectl -n "$NS" patch deploy "$WORKLOAD" --type=json \
        -p "[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/securityContext\",\"value\":${BEFORE}}]" >/dev/null 2>&1 || true
    fi
  fi
fi

# The certificate task's output. The Ingress keeps its tls block otherwise, and
# traefik then serves a secret that no longer exists.
kubectl -n "$NS" patch ingress "$WORKLOAD" --type=json \
  -p '[{"op":"remove","path":"/spec/tls"}]' >/dev/null 2>&1 || true
kubectl -n "$NS" delete certificate "${WORKLOAD}-tls" --ignore-not-found >/dev/null 2>&1 || true
kubectl -n "$NS" delete secret "${WORKLOAD}-tls-secret" --ignore-not-found >/dev/null 2>&1 || true
kubectl -n "$NS" delete policyexception legacy-reporter-exemption --ignore-not-found >/dev/null 2>&1 || true
echo "Removed the certificate, the ingress TLS wiring and the policy exception."

kubectl -n "$NS" delete configmap "$STATE" --ignore-not-found >/dev/null 2>&1 || true
