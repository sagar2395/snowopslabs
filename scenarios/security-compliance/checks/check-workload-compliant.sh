#!/usr/bin/env bash
set -euo pipefail

# Grades the remediation half of the drill: the go-api Deployment must satisfy
# all four ClusterPolicies at the source — in the Deployment's pod template,
# not by being excluded from the policy.
#
# This reads .spec.template.spec, not the running Pod, on purpose. A mesh
# sidecar injected by a webhook is not the learner's workload and is not what a
# real remediation would change; the Deployment is the file a team edits.

NS="go-api"
DEPLOY="go-api"
NOTES=""

note() { NOTES="${NOTES}  - $1
"; }

tpl() { kubectl -n "$NS" get deployment "$DEPLOY" -o jsonpath="$1" 2>/dev/null || echo ""; }

if ! kubectl -n "$NS" get deployment "$DEPLOY" >/dev/null 2>&1; then
  echo "FAIL: deployment/$DEPLOY not found in $NS." >&2
  echo "      Deploy it: labctl app build go-api && labctl app deploy go-api" >&2
  exit 1
fi

names=$(tpl '{range .spec.template.spec.containers[*]}{.name}{"\n"}{end}')
i=0
for c in $names; do
  esc=$(tpl "{.spec.template.spec.containers[$i].securityContext.allowPrivilegeEscalation}")
  nonroot=$(tpl "{.spec.template.spec.containers[$i].securityContext.runAsNonRoot}")
  cpu=$(tpl "{.spec.template.spec.containers[$i].resources.limits.cpu}")
  mem=$(tpl "{.spec.template.spec.containers[$i].resources.limits.memory}")
  image=$(tpl "{.spec.template.spec.containers[$i].image}")

  [ "$esc" = "false" ] || note "container '$c': securityContext.allowPrivilegeEscalation is '${esc:-unset}', must be false (policy deny-privilege-escalation)."
  [ "$nonroot" = "true" ] || note "container '$c': securityContext.runAsNonRoot is '${nonroot:-unset}', must be true (policy require-non-root-user)."
  [ -n "$cpu" ] || note "container '$c': resources.limits.cpu is unset (policy require-resource-limits)."
  [ -n "$mem" ] || note "container '$c': resources.limits.memory is unset (policy require-resource-limits)."
  case "$image" in
    *:latest) note "container '$c': image '$image' uses the :latest tag (policy disallow-latest-tag)." ;;
    *:*) ;;
    *) note "container '$c': image '$image' has no explicit tag, which resolves to :latest (policy disallow-latest-tag)." ;;
  esac
  i=$((i + 1))
done

if [ -n "$NOTES" ]; then
  echo "FAIL: deployment/$DEPLOY still violates the Pod Security policies:" >&2
  printf '%s' "$NOTES" >&2
  echo "  Fix the workload, not the policy. Patch the Deployment and let it roll out:" >&2
  echo "    kubectl -n $NS patch deployment $DEPLOY --type=json -p '[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/securityContext/runAsNonRoot\",\"value\":true}]'" >&2
  echo "    kubectl -n $NS rollout status deployment/$DEPLOY" >&2
  echo "  Then re-read the report:  kubectl -n $NS get policyreports" >&2
  exit 1
fi

echo "deployment/$DEPLOY satisfies all four policies at the source: non-root,"
echo "no privilege escalation, cpu+memory limits, explicit image tag."
