#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Install idempotency for the default-stack providers a reviewer's core loop
# always runs (ingress + monitoring). The install must use `helm upgrade
# --install` - the idempotent primitive - so a re-run after an interrupted
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

# kind has no LoadBalancer implementation, so helm --wait on a LoadBalancer
# Service never returns there.
@test "traefik on kind binds the ingress node's host ports, not a LoadBalancer" {
  PROFILE=kind run bash "$ROOT/platform/ingress/traefik/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "service.spec.type=ClusterIP"
  assert_called helm "ports.web.hostPort=80"
  assert_called helm "ports.websecure.hostPort=443"
  assert_called helm "nodeSelector.ingress-ready=true"
  assert_called helm "key=node-role.kubernetes.io/control-plane"
}

@test "traefik on k3d keeps the LoadBalancer from its values file" {
  PROFILE=k3d run bash "$ROOT/platform/ingress/traefik/install.sh"
  [ "$status" -eq 0 ]
  assert_called helm "upgrade --install traefik"
  refute_called helm "hostPort"
  refute_called helm "service.spec.type"
  grep -A2 '^service:' "$ROOT/platform/ingress/traefik/values.yaml" | grep -q 'type: LoadBalancer'
}

# A component's volume uses the runtime's StorageClass (STORAGE_CLASS, from
# runtimes/<profile>/runtime.env). A class named in a values file exists on one
# runtime only, so the claim never binds on the others.
@test "grafana, loki and tempo claim volumes from the runtime's storage class" {
  for c in "monitoring/grafana|persistence.storageClassName=standard" \
    "logging/loki|singleBinary.persistence.storageClass=standard" \
    "tracing/tempo|persistence.storageClassName=standard"; do
    reset_calls
    STORAGE_CLASS=standard run bash "$ROOT/platform/${c%%|*}/install.sh"
    [ "$status" -eq 0 ] || { echo "${c%%|*}: $output"; false; }
    assert_called helm "${c#*|}"
  done
}

@test "no platform values file names a storage class of one runtime" {
  run grep -rln "local-path" "$ROOT/platform" --include=values.yaml
  [ "$status" -ne 0 ] || { echo "hardcoded storage class in: $output"; false; }
}

# The external-secrets chart's ServiceMonitor template brings its own
# external-secrets-metrics Service; metrics.service makes a second one, and on
# a lab with Prometheus the install fails with "already exists".
@test "external-secrets turns on the ServiceMonitor without a second metrics Service" {
  values="$ROOT/platform/secrets/external-secrets/values.yaml"
  awk '/^serviceMonitor:/ { on = 1; next } on && /^[^ ]/ { exit } on && /enabled: true/ { found = 1 } END { exit !found }' "$values"
  run awk '/^metrics:/ { m = 1; next } m && /^[^ ]/ { m = 0 } m && /^  service:/ { s = 1; next } s && /^  [^ ]/ { s = 0 } s && /enabled: true/ { print; found = 1 } END { exit !found }' "$values"
  [ "$status" -ne 0 ] || { echo "metrics.service.enabled is true in ${values#"$ROOT"/}"; false; }
}

# client-go in helm and kubectl prints an error for each API group the
# apiserver cannot serve yet: the metrics API while metrics-server starts, and
# the CRDs a chart has just installed. labctl shows them only with --verbose.
discovery_noise_stub() {
  stub_script "$1" <<'EOF'
case "$*" in
  upgrade* | apply* | rollout*)
    echo "E1029 10:00:00.000000    4242 memcache.go:287] couldn't get resource list for metrics.k8s.io/v1beta1: the server is currently unable to handle the request" >&2
    echo "E1029 10:00:01.000000    4242 memcache.go:265] \"Unhandled Error\" err=\"couldn't get resource list for monitoring.coreos.com/v1: the server could not find the requested resource\" logger=\"UnhandledError\"" >&2
    if [ -n "${TOOL_FAILS:-}" ]; then
      echo "Error: $1 failed: the real problem" >&2
      exit 1
    fi
    echo "$1 done"
    ;;
esac
EOF
}

@test "helm and kubectl discovery errors are left out of the install output" {
  discovery_noise_stub helm
  discovery_noise_stub kubectl
  for c in ingress/traefik monitoring/metrics/prometheus; do
    reset_calls
    run bash "$ROOT/platform/$c/install.sh"
    [ "$status" -eq 0 ] || { echo "$c: $output"; false; }
    [[ "$output" == *"upgrade done"* ]] || { echo "$c: $output"; false; }
    [[ "$output" != *"couldn't get resource list"* ]] || { echo "$c: $output"; false; }
  done
}

@test "a real helm error still shows and still fails the install" {
  discovery_noise_stub helm
  TOOL_FAILS=1 run bash "$ROOT/platform/monitoring/metrics/prometheus/install.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Error: upgrade failed: the real problem"* ]] || false
  [[ "$output" != *"couldn't get resource list"* ]] || false
}

@test "a real kubectl error still shows and still fails the install" {
  discovery_noise_stub kubectl
  TOOL_FAILS=1 run bash "$ROOT/platform/monitoring/metrics/prometheus/install.sh"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Error: apply failed: the real problem"* ]] || false
  [[ "$output" != *"couldn't get resource list"* ]] || false
}

@test "LABCTL_VERBOSE=1 keeps the discovery errors" {
  discovery_noise_stub helm
  discovery_noise_stub kubectl
  LABCTL_VERBOSE=1 run bash "$ROOT/platform/ingress/traefik/install.sh"
  [ "$status" -eq 0 ]
  [[ "$output" == *"memcache.go:287] couldn't get resource list for metrics.k8s.io/v1beta1"* ]] || false
  [[ "$output" == *"couldn't get resource list for monitoring.coreos.com/v1: the server could not find"* ]] || false
}

# The first upgrade fails on an immutable StatefulSet field; the retry runs
# after the controller is deleted. Both print discovery errors.
statefulset_retry_helm() {
  stub_script helm <<'EOF'
case "$*" in
  upgrade*)
    echo "E1029 10:00:00.000000    4242 memcache.go:287] couldn't get resource list for metrics.k8s.io/v1beta1: the server is currently unable to handle the request" >&2
    if [ ! -f "$STUB_DIR/retried" ]; then
      touch "$STUB_DIR/retried"
      echo "Error: UPGRADE FAILED: cannot patch \"loki\" with kind StatefulSet: StatefulSet.apps \"loki\" is invalid: spec: Forbidden: updates to statefulset spec for fields other than 'replicas', 'ordinals', 'template' are forbidden" >&2
      exit 1
    fi
    if [ -n "${RETRY_FAILS:-}" ]; then
      echo "Error: UPGRADE FAILED: the retry failed too" >&2
      exit 1
    fi
    echo "upgrade done"
    ;;
esac
EOF
}

@test "the StatefulSet retry works with the discovery filter in place" {
  statefulset_retry_helm
  run bash -c 'set -euo pipefail; . "$1/platform/_lib/helm.sh"; helm_upgrade_install loki logging grafana-community/loki --namespace logging' _ "$ROOT"
  [ "$status" -eq 0 ]
  assert_called kubectl "delete statefulset loki --namespace logging --cascade=orphan"
  assert_call_count helm 2 "upgrade --install loki"
  [[ "$output" == *"upgrade done"* ]] || false
  [[ "$output" != *"couldn't get resource list"* ]] || false
}

@test "a failed StatefulSet retry still fails and shows helm's error" {
  statefulset_retry_helm
  RETRY_FAILS=1 run bash -c 'set -euo pipefail; . "$1/platform/_lib/helm.sh"; helm_upgrade_install loki logging grafana-community/loki --namespace logging' _ "$ROOT"
  [ "$status" -ne 0 ]
  [[ "$output" == *"the retry failed too"* ]] || false
  [[ "$output" != *"couldn't get resource list"* ]] || false
}
