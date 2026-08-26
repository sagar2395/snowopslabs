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
