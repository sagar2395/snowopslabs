#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Helm 4 upgrades apply server-side, so a field last changed with kubectl belongs
# to kubectl and `labctl app deploy` would refuse to restore it. Redeploying
# from the chart must win, which helm 4 does with --force-conflicts; helm 3 has
# no such flag and already overwrites hand edits.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command docker k3d kind helm kubectl
  ROOT="$(project_root)"
  HELM_SH="$ROOT/src/engine/deploy/helm.sh"

  CONTENT="$STUB_DIR/content"
  mkdir -p "$CONTENT/apps/testapp/deploy/helm"
  cat >"$CONTENT/apps/testapp/app.env" <<'APPENV'
APP_NAME=testapp
BUILD_STRATEGY=docker
DEPLOY_STRATEGY=helm
HELM_RELEASE_NAME=testapp
HELM_VALUES=values.yaml
APPENV
  : >"$CONTENT/apps/testapp/deploy/helm/values.yaml"
  stub_when docker "image inspect" 0 "sha256:cafebabe"
  cd "$CONTENT"
}

teardown() {
  stub_teardown
}

@test "helm 4 redeploys take back fields a learner changed with kubectl" {
  stub_when helm "version" 0 "v4.2.3+g43e8b7f"

  run bash "$HELM_SH" deploy testapp
  [ "$status" -eq 0 ]
  assert_called helm "--force-conflicts"
}

@test "helm 3 is not given the server-side flag it does not have" {
  stub_when helm "version" 0 "v3.14.4+g81c902a"

  run bash "$HELM_SH" deploy testapp
  [ "$status" -eq 0 ]
  if calls_for helm | grep -q -- "--force-conflicts"; then
    printf '\nhelm 3 was passed --force-conflicts:\n%s\n' "$(calls_for helm)" >&2
    return 1
  fi
}
