#!/usr/bin/env bash
set -euo pipefail

# Grades the triage half of a compliance rollout: not every violation is fixed,
# and the ones you cannot fix must be exempted NARROWLY.
#
# Three assertions, and the third is the one that matters:
#   1. legacy-reporter is actually running — an exemption that leaves the
#      workload down solved nothing.
#   2. A Pod carrying the exemption label is ADMITTED even though it runs as
#      root with no limits, so the exception really covers the workload.
#   3. The same Pod WITHOUT the label is still REJECTED. This is what separates
#      a scoped exception from a hole: widening the exception to the namespace,
#      demoting the policy, or adding go-api to the policy's exclude list all
#      satisfy (1) and (2) and fail here.

NS="go-api"
DEPLOY="legacy-reporter"
LABEL_KEY="compliance.snowops.net/exempt"
LABEL_VAL="vendor-image"

if ! kubectl -n "$NS" get deployment "$DEPLOY" >/dev/null 2>&1; then
  echo "FAIL: deployment/$DEPLOY not found in $NS." >&2
  echo "      Re-run: labctl scenario up security-compliance" >&2
  exit 1
fi

ready=$(kubectl -n "$NS" get deployment "$DEPLOY" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "")
if [ "${ready:-0}" -lt 1 ] 2>/dev/null || [ -z "$ready" ]; then
  echo "FAIL: deployment/$DEPLOY has no ready replicas." >&2
  echo "      Under Enforce its pods are rejected until an exemption covers them." >&2
  echo "      See why: kubectl -n $NS describe replicaset -l app=$DEPLOY | tail -20" >&2
  exit 1
fi

# Nothing here is meaningful while the policies are still in Audit — no Pod is
# rejected, so "the exemption is too wide" would be a misleading way to say
# "you have not promoted yet". Say the true thing instead.
audit=""
for pol in deny-privilege-escalation require-non-root-user; do
  action=$(kubectl get clusterpolicy "$pol" -o jsonpath='{.spec.rules[0].validate.failureAction}' 2>/dev/null || echo "")
  [ "$action" = "Enforce" ] || audit="$audit $pol"
done
if [ -n "$audit" ]; then
  echo "FAIL: an exemption only means something once something is being enforced," >&2
  echo "      and these are still in Audit:$audit" >&2
  echo "      Do the promotion step first — 'policies-enforcing' grades it — then come back" >&2
  echo "      to exempting $DEPLOY." >&2
  exit 1
fi

# A pod spec that violates both enforced policies. Applied twice: once wearing
# the exemption label, once bare.
probe() { # $1 = pod name, $2 = extra labels block
  kubectl apply -n "$NS" --dry-run=server -f - >/dev/null 2>&1 <<POD
apiVersion: v1
kind: Pod
metadata:
  name: $1
  labels:
    app: exception-probe
$2
spec:
  containers:
    - name: probe
      image: go-api:v1.2.0
      securityContext:
        runAsNonRoot: false
        allowPrivilegeEscalation: true
POD
}

if ! probe exception-probe-labelled "    $LABEL_KEY: $LABEL_VAL"; then
  echo "FAIL: a Pod labelled $LABEL_KEY=$LABEL_VAL is still rejected, so nothing" >&2
  echo "      exempts $DEPLOY from the enforced policies." >&2
  echo "      Apply the PolicyException snippet from 'labctl scenario info security-compliance'." >&2
  echo "      If you already did, check Kyverno accepted it and that ruleNames include the" >&2
  echo "      autogen- variants:  kubectl -n $NS get polex -o yaml" >&2
  exit 1
fi

if probe exception-probe-bare ""; then
  echo "FAIL: the exemption is too wide — a Pod with NO exemption label was also" >&2
  echo "      admitted while running as root with allowPrivilegeEscalation: true." >&2
  echo "      Enforcement is off for the whole namespace, not just for $DEPLOY. Check for:" >&2
  echo "        - a PolicyException matching the namespace instead of the label" >&2
  echo "        - go-api added to a policy's exclude list" >&2
  echo "        - a policy quietly demoted back to Audit" >&2
  echo "      kubectl -n $NS get polex -o yaml; kubectl get clusterpolicy -o yaml | grep -A6 exclude" >&2
  exit 1
fi

echo "$DEPLOY runs under a scoped exemption: labelled pods are admitted, unlabelled"
echo "pods running as root are still rejected."
