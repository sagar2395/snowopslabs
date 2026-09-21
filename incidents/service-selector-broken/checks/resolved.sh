#!/usr/bin/env bash
# Passes (exit 0) when the fault is resolved: the Service selects the workload's
# own pods again, and the workload answers through the ingress.
#
# Both halves are needed, and the second alone is what this check used to be.
# An HTTP probe cannot tell a repaired Service from a Service that is still
# wrong but happens to be answered — relabel one pod to match the broken
# selector and the endpoints repopulate, the URL returns 200, and the check
# passes while the Deployment still labels its pods the other way. The next
# rollout then breaks the app again, for a learner who was told they had fixed
# it. Grade the selector, and use the probe to prove it actually took.
set -euo pipefail

NS="${TARGET_NAMESPACE:-go-api}"
SVC="${TARGET_WORKLOAD:-go-api}"
DEPLOY="${TARGET_WORKLOAD:-go-api}"
SUFFIX="${DOMAIN_SUFFIX:-k3d.local}"

SELECTOR="$(kubectl -n "$NS" get svc "$SVC" -o 'jsonpath={.spec.selector}' 2>/dev/null || true)"
if [ -z "$SELECTOR" ] || [ "$SELECTOR" = "{}" ]; then
  echo "FAIL: Service $NS/$SVC has no selector at all, so it can never match a pod." >&2
  exit 1
fi

# The Deployment's pod template is the authority on how its pods are labelled.
LABELS="$(kubectl -n "$NS" get deploy "$DEPLOY" -o 'jsonpath={.spec.template.metadata.labels}' 2>/dev/null || true)"
if [ -z "$LABELS" ]; then
  echo "FAIL: deployment $NS/$DEPLOY not found — cannot tell which pods the Service should select." >&2
  exit 1
fi

# Every selector key must appear in the pod template with the same value: a
# selector is an AND, so one wrong key matches nothing however right the rest are.
# printf '%s\n', not '%s': without the trailing newline `read` discards the last
# field, and the last field is exactly where a single wrong key hides.
MISMATCH="$(printf '%s\n' "$SELECTOR" | tr -d '{}"' | tr ',' '\n' |
  while IFS=':' read -r key want; do
    [ -n "${key:-}" ] || continue
    got="$(printf '%s\n' "$LABELS" | tr -d '{}"' | tr ',' '\n' |
      awk -F: -v k="$key" '$1 == k { print $2; exit }')"
    [ "$got" = "$want" ] || echo "    ${key}: Service wants \"${want}\", pods are labelled \"${got:-<absent>}\""
  done)"

if [ -n "$MISMATCH" ]; then
  echo "FAIL: $NS/$SVC selects labels the workload's pods do not carry:" >&2
  printf '%s\n' "$MISMATCH" >&2
  echo "  Stuck? 'labctl incident hint' walks you in." >&2
  exit 1
fi

CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "http://${SVC}.${SUFFIX}/health" || echo 000)"
if [ "$CODE" != "200" ]; then
  echo "FAIL: the selector matches, but http://${SVC}.${SUFFIX}/health returned ${CODE}." >&2
  echo "  Endpoints can take a few seconds to repopulate — try again shortly." >&2
  exit 1
fi

echo "OK: $NS/$SVC selects the workload's own pods, and it answers 200 through the ingress."
