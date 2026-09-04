#!/usr/bin/env bash
set -euo pipefail

# Grades the Audit -> Enforce promotion, the half of policy work that actually
# changes cluster behaviour.
#
# Two assertions, because either alone is a tautology:
#   1. The two Pod Security policies declare failureAction: Enforce.
#   2. The admission webhook REALLY rejects a non-compliant Pod. This is proved
#      with `kubectl apply --dry-run=server`, which runs the full admission
#      chain without creating anything — so it fails if Kyverno is down, if the
#      webhook was removed, or if the policy was narrowed to exclude go-api.
#      A policy object that exists but does not bite cannot pass this.

POLICIES="deny-privilege-escalation require-non-root-user"
NS="go-api"
fail=0

for p in $POLICIES; do
  action=$(kubectl get clusterpolicy "$p" \
    -o jsonpath='{.spec.rules[0].validate.failureAction}' 2>/dev/null || echo "")
  # Fall back to the deprecated policy-level field so a hand-edited policy that
  # used it is still read correctly rather than reported as missing.
  if [ -z "$action" ]; then
    action=$(kubectl get clusterpolicy "$p" \
      -o jsonpath='{.spec.validationFailureAction}' 2>/dev/null || echo "")
  fi

  if [ -z "$action" ]; then
    echo "FAIL: ClusterPolicy '$p' not found. Re-run 'labctl scenario up security-compliance'." >&2
    fail=1
  elif [ "$action" != "Enforce" ]; then
    echo "FAIL: ClusterPolicy '$p' is still in $action mode." >&2
    echo "      Promote it: kubectl patch clusterpolicy $p --type=json \\" >&2
    echo "        -p '[{\"op\":\"replace\",\"path\":\"/spec/rules/0/validate/failureAction\",\"value\":\"Enforce\"}]'" >&2
    fail=1
  fi
done

[ "$fail" -eq 0 ] || exit 1

# The behavioural half: a Pod that violates both policies must be refused.
rejected=$(
  kubectl apply -n "$NS" --dry-run=server -f - >/dev/null 2>&1 <<'POD' && echo no || echo yes
apiVersion: v1
kind: Pod
metadata:
  name: compliance-probe
spec:
  containers:
    - name: probe
      image: nginx:1.27
      securityContext:
        runAsNonRoot: false
        allowPrivilegeEscalation: true
      resources:
        limits:
          cpu: 50m
          memory: 32Mi
POD
)

if [ "$rejected" != "yes" ]; then
  echo "FAIL: the policies say Enforce, but the admission webhook ACCEPTED a Pod that" >&2
  echo "      runs as root with allowPrivilegeEscalation: true in namespace $NS." >&2
  echo "      Enforce is not reaching this namespace. Check that Kyverno is up and that" >&2
  echo "      $NS is not in the policies' exclude list:" >&2
  echo "        kubectl -n kyverno get pods" >&2
  echo "        kubectl get clusterpolicy require-non-root-user -o yaml | grep -A12 exclude" >&2
  exit 1
fi

echo "Enforce is live: both Pod Security policies are in Enforce and the admission"
echo "webhook rejected a root/privilege-escalating Pod in $NS."
