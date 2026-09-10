#!/usr/bin/env bash
# Passes (exit 0) when the fault is resolved: nothing in the namespace is
# black-holing ingress to the workload, and the workload answers through the
# ingress again.
#
# An HTTP probe alone is not enough. The ingress controller holds a keep-alive
# pool to the backend, and a NetworkPolicy only governs NEW connections — so the
# probe can return 200 over a connection established before the policy landed,
# while every fresh connection is refused. That is exactly how this fault used
# to report itself resolved the moment it was injected. Grade the policy, and
# use the probe to prove the traffic really flows.
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"

# Look for the fault's OWN policy, by its label, rather than for any deny-all.
# A namespace-wide deny baseline with targeted allows beside it is the pattern
# the security-compliance scenario teaches and production uses — flagging every
# empty-ingress policy would make this incident unresolvable whenever that
# scenario is active, which is a legitimate thing for a learner to have running.
#
# A policy that lists no ingress peers admits nobody, and no other policy can
# rescue the traffic it does not name, because ingress rules are a union of what
# the policies allow: adding a permissive policy beside this one restores the
# service while leaving the trap in place for whoever removes that policy next.
DENYING="$(kubectl -n "$NS" get networkpolicy -l labfault \
  -o 'jsonpath={range .items[*]}{.metadata.name}{"|"}{.spec.policyTypes}{"|"}{.spec.ingress}{"\n"}{end}' 2>/dev/null |
  awk -F'|' '$2 ~ /Ingress/ && ($3 == "" || $3 == "[]") { print "    " $1 }')"

if [ -n "$DENYING" ]; then
  echo "FAIL: a NetworkPolicy in ${NS} admits no ingress traffic at all:" >&2
  printf '%s\n' "$DENYING" >&2
  echo "  Stuck? 'labctl incident hint' walks you in." >&2
  exit 1
fi

CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "http://${DEPLOY}.${SUFFIX}/health" || echo 000)"
if [ "$CODE" != "200" ]; then
  echo "FAIL: no policy is blocking ingress, but http://${DEPLOY}.${SUFFIX}/health returned ${CODE}." >&2
  echo "  The ingress controller may still be retrying failed connections — try again shortly." >&2
  exit 1
fi

echo "OK: nothing is blocking ingress to ${NS}/${DEPLOY}, and it answers 200 through the ingress."
