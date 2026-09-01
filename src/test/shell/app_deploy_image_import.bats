#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# `app deploy` must ensure a docker-built image is present inside a local
# cluster runtime (k3d/kind) before deploying. Without this, a deploy after a
# cluster re-create — which wipes previously-imported images — leaves the pod
# stuck in ImagePullBackOff trying to pull the never-published image from a
# registry. These tests pin the import behaviour per runtime.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command docker k3d kind helm kubectl
  ROOT="$(project_root)"
  DEPLOY="$ROOT/src/engine/deploy.sh"

  # Minimal content root: deploy.sh resolves apps/<name>/app.env from the CWD.
  CONTENT="$STUB_DIR/content"
  mkdir -p "$CONTENT/apps/testapp/deploy/helm"
  cat >"$CONTENT/apps/testapp/app.env" <<'EOF'
APP_NAME=testapp
BUILD_STRATEGY=docker
DEPLOY_STRATEGY=helm
HELM_RELEASE_NAME=testapp
HELM_VALUES=values.yaml
EOF
  : >"$CONTENT/apps/testapp/deploy/helm/values.yaml"
  cd "$CONTENT"
}

teardown() {
  stub_teardown
}

@test "k3d deploy imports the image when it is present in the local daemon" {
  PROFILE=k3d CLUSTER_NAME=snowops run bash "$DEPLOY" deploy testapp
  [ "$status" -eq 0 ]
  assert_called k3d "image import"
  assert_called k3d "testapp:latest"
  assert_called k3d "snowops"
}

@test "k3d deploy builds the image first when it is missing locally" {
  # Make only `docker image inspect` fail, so the guard falls back to a build.
  stub_when docker "inspect" 1
  PROFILE=k3d CLUSTER_NAME=snowops run bash "$DEPLOY" deploy testapp
  [ "$status" -eq 0 ]
  assert_called docker "build"
}

@test "kind deploy loads the image into the named kind cluster" {
  PROFILE=kind CLUSTER_NAME=snowops run bash "$DEPLOY" deploy testapp
  [ "$status" -eq 0 ]
  assert_called kind "load docker-image"
  assert_called kind "testapp:latest"
}

@test "incluster deploy does not import or load (registry-backed)" {
  PROFILE=incluster run bash "$DEPLOY" deploy testapp
  [ "$status" -eq 0 ]
  refute_called k3d "image import"
  refute_called kind "load docker-image"
}

@test "destroy never imports an image" {
  PROFILE=k3d run bash "$DEPLOY" destroy testapp
  [ "$status" -eq 0 ]
  refute_called k3d "image import"
}
