#!/usr/bin/env bash
set -euo pipefail

# Grades the isolation POSTURE of the go-api namespace, not the presence of a
# NetworkPolicy object.
#
# The distinction matters: NetworkPolicies are additive, so `default-deny-all`
# can sit there untouched while a second policy re-opens the namespace to
# everything. An existence check stays green through that; this one does not.
#
# Everything here is kubectl and POSIX shell — no jq, no python.

NS="go-api"
MON="${MONITORING_NAMESPACE:-monitoring}"
NOTES=""

note() {
  NOTES="${NOTES}  - $1
"
}

if [ "$(kubectl -n "$NS" get networkpolicies -o name 2>/dev/null | wc -l | tr -d ' ')" = "0" ]; then
  echo "FAIL: no NetworkPolicies in namespace $NS — the namespace is wide open." >&2
  echo "      Re-run: labctl scenario up security-compliance" >&2
  exit 1
fi

# 1. The deny baseline: selects every pod, both directions, and carries no
#    allow rules of its own.
if ! kubectl -n "$NS" get networkpolicy default-deny-all >/dev/null 2>&1; then
  note "default-deny-all is missing — nothing establishes the deny baseline."
else
  selector=$(kubectl -n "$NS" get networkpolicy default-deny-all -o jsonpath='{.spec.podSelector}')
  types=$(kubectl -n "$NS" get networkpolicy default-deny-all -o jsonpath='{.spec.policyTypes}')
  rules=$(kubectl -n "$NS" get networkpolicy default-deny-all -o jsonpath='{.spec.ingress}{.spec.egress}')

  case "$selector" in
    "" | "{}") ;;
    *) note "default-deny-all has a podSelector ($selector), so it denies only some pods; it must select every pod with 'podSelector: {}'." ;;
  esac
  case "$types" in
    *Ingress*) ;;
    *) note "default-deny-all does not cover Ingress; inbound traffic is unrestricted." ;;
  esac
  case "$types" in
    *Egress*) ;;
    *) note "default-deny-all does not cover Egress; outbound traffic is unrestricted." ;;
  esac
  [ -z "$rules" ] || note "default-deny-all carries allow rules; a deny baseline must have none."
fi

# 2. No policy in the set cancels the baseline. An ingress rule with no 'from'
#    admits every source; an egress rule with neither 'to' nor 'ports' permits
#    every destination. Either one makes default-deny-all decorative.
# shellcheck disable=SC2016  # $name is a go-template variable, not a shell one
open=$(kubectl -n "$NS" get networkpolicies -o go-template='
{{- range .items -}}
  {{- $name := .metadata.name -}}
  {{- range .spec.ingress -}}
    {{- if not .from -}}{{ $name }} accepts traffic from ANY source (an ingress rule with no "from"){{ "\n" }}{{- end -}}
  {{- end -}}
  {{- range .spec.egress -}}
    {{- if and (not .to) (not .ports) -}}{{ $name }} permits traffic to ANY destination (an egress rule with neither "to" nor "ports"){{ "\n" }}{{- end -}}
  {{- end -}}
{{- end -}}')
if [ -n "$open" ]; then
  while IFS= read -r line; do
    [ -z "$line" ] || note "$line — it cancels default-deny-all."
  done <<EOF
$open
EOF
fi

# 3. The allowances the rest of the scenario depends on. Isolation that also
#    breaks DNS, the ingress path or scraping is a misconfiguration, not a win.
egress_ports=$(kubectl -n "$NS" get networkpolicies -o jsonpath='{.items[*].spec.egress[*].ports[*].port}')
ingress_from=$(kubectl -n "$NS" get networkpolicies -o jsonpath='{.items[*].spec.ingress[*].from[*].namespaceSelector.matchLabels}')

case " $egress_ports " in
  *" 53 "*) ;;
  *) note "no egress rule allows DNS (port 53); pods in $NS cannot resolve any name." ;;
esac
case "$ingress_from" in
  *'"traefik"'*) ;;
  *) note "no ingress rule allows the traefik namespace; the app is unreachable through the ingress controller." ;;
esac
case "$ingress_from" in
  *"\"$MON\""*) ;;
  *) note "no ingress rule allows the $MON namespace; Prometheus cannot scrape the app and the dashboards go blank." ;;
esac

if [ -n "$NOTES" ]; then
  echo "FAIL: namespace $NS is not isolated as the scenario claims:" >&2
  printf '%s' "$NOTES" >&2
  echo "  Restore the intended policy set:" >&2
  echo "    kubectl -n $NS delete networkpolicy --all && labctl scenario up security-compliance" >&2
  exit 1
fi

echo "Namespace $NS is default-deny in both directions, with DNS, ingress and"
echo "$MON scraping explicitly re-opened and no rule cancelling the baseline."
