#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Install idempotency for the default-stack providers a reviewer's core loop
# always runs (ingress + monitoring). The install must use `helm upgrade
# --install` — the idempotent primitive — so a re-run after an interrupted
# `labctl init` converges instead of erroring (golden rule 5).

load 'helpers/stub'

setup() {
  stub_setup
  stub_command kubectl helm
  ROOT="$(project_root)"
}

teardown() {
  stub_teardown
}

@test "traefik install uses helm upgrade --install" {
  run bash "$ROOT/platform/ingress/traefik/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "upgrade --install"
}

@test "traefik install is safe to run twice (converges)" {
  run bash "$ROOT/platform/ingress/traefik/install.sh"
  [ "$status" -eq 0 ]
  reset_calls
  run bash "$ROOT/platform/ingress/traefik/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "upgrade --install"
}

@test "prometheus install uses helm upgrade --install" {
  run bash "$ROOT/platform/monitoring/metrics/prometheus/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "upgrade --install"
}

@test "prometheus install is safe to run twice (converges)" {
  run bash "$ROOT/platform/monitoring/metrics/prometheus/install.sh"
  [ "$status" -eq 0 ]
  reset_calls
  run bash "$ROOT/platform/monitoring/metrics/prometheus/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "upgrade --install"
}

@test "grafana install uses helm upgrade --install" {
  run bash "$ROOT/platform/monitoring/grafana/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "upgrade --install grafana"
}

# A helm process that dies mid-operation leaves the release pending, and every
# later upgrade fails with "another operation ... is in progress" until it is
# cleared.
@test "a release stuck in pending-install is uninstalled before installing" {
  stub_when helm "status grafana" 0 "STATUS: pending-install"
  run bash "$ROOT/platform/monitoring/grafana/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "uninstall grafana --namespace monitoring"
  refute_called helm "rollback"
  assert_called helm "upgrade --install grafana"
}

@test "a release stuck in pending-upgrade is rolled back before upgrading" {
  stub_when helm "status traefik" 0 "STATUS: pending-upgrade"
  run bash "$ROOT/platform/ingress/traefik/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "rollback traefik --namespace traefik"
  refute_called helm "uninstall"
  assert_called helm "upgrade --install traefik"
}

@test "a deployed release is upgraded without being cleared" {
  stub_when helm "status prometheus" 0 "STATUS: deployed"
  run bash "$ROOT/platform/monitoring/metrics/prometheus/install.sh"
  [ "$status" -eq 0 ]
  refute_called helm "uninstall"
  refute_called helm "rollback"
  assert_called helm "upgrade --install prometheus prometheus-community/kube-prometheus-stack"
}

@test "a failed clear stops the install" {
  stub_when helm "status grafana" 0 "STATUS: pending-install"
  stub_when helm "uninstall grafana" 1
  run bash "$ROOT/platform/monitoring/grafana/install.sh"
  [ "$status" -ne 0 ]
  refute_called helm "upgrade --install"
}
