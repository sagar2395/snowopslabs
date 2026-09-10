#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# The dashboard's Ingress backend is a cluster-wide hazard, not a local one.
# The chart ships kong.proxy.http.enabled=false, so turning TLS off without
# turning HTTP on leaves Kong with no listener and the chart renders no proxy
# Service. The Ingress then names a Service that does not exist, and traefik
# retries it in a hot loop until it fails its own liveness probe — taking every
# other ingress in the lab down with it.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command kubectl helm
  SCRIPT="$(project_root)/platform/dashboard/kubernetes-dashboard/install.sh"
}

teardown() { stub_teardown; }

@test "the Ingress is created against the proxy Service the cluster reports" {
  stub_when kubectl "jsonpath={.spec.ports[0].port}" 0 "80"
  stub_when kubectl "get svc" 0 "service/kubernetes-dashboard-kong-proxy"

  run bash "$SCRIPT"
  [ "$status" -eq 0 ]
  # Two stdin applies: the namespace, then the Ingress.
  [ "$(call_count kubectl "apply -f -")" -eq 2 ]
}

@test "no Ingress is created when the proxy Service is absent" {
  # Default stub: 'kubectl get svc' returns nothing, which is exactly the
  # broken-values case.
  run bash "$SCRIPT"
  [ "$status" -ne 0 ]
  # Only the namespace apply: the script stops before the Ingress.
  [ "$(call_count kubectl "apply -f -")" -eq 1 ]
}

@test "the failure names the value that causes it" {
  run bash "$SCRIPT"
  [ "$status" -ne 0 ]
  echo "$output" | grep -q "kong.proxy"
  echo "$output" | grep -q "traefik"
}

@test "values enable Kong's plain-HTTP listener, not just disable TLS" {
  values="$(project_root)/platform/dashboard/kubernetes-dashboard/values.yaml"
  grep -q 'http:' "$values"
  # A listener has to be enabled for the chart to render a proxy Service at all.
  run grep -A1 'http:' "$values"
  echo "$output" | grep -q 'enabled: true'
}
