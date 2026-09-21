#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# A rebuild under a mutable tag (:latest, or a tag reused while iterating) leaves
# the Deployment spec byte-identical, so `helm upgrade` is a no-op, no new
# ReplicaSet appears, and the running pods keep serving the previous build —
# while deploy reports success. Verified live before this was fixed: the node
# held the new image, the pod held the old one, and /version still reported the
# previous build.
#
# The fix threads the local image ID into a pod annotation, so the template
# changes exactly when the image content does. These tests pin both halves:
# the ID is passed when docker can resolve it, and nothing is forced when it
# cannot.

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
  cd "$CONTENT"
}

teardown() {
  stub_teardown
}

@test "deploy passes the local image ID so a rebuilt image actually rolls out" {
  stub_when docker "image inspect" 0 "sha256:cafebabe"

  run bash "$HELM_SH" deploy testapp
  [ "$status" -eq 0 ]
  assert_called helm "image.id=sha256:cafebabe"
}

@test "the image ID is read from the tag the app is built under" {
  stub_when docker "image inspect" 0 "sha256:cafebabe"

  run bash "$HELM_SH" deploy testapp
  [ "$status" -eq 0 ]
  assert_called docker "testapp:latest"
}

@test "an unresolvable image leaves the annotation empty rather than failing the deploy" {
  # docker cannot resolve the reference — a remote-only build, or an image
  # already in the cluster but not in the local daemon. Deploy must still work.
  stub_when docker "image inspect" 1 ""

  run bash "$HELM_SH" deploy testapp
  [ "$status" -eq 0 ]
  assert_called helm "image.id="
  # ...and empty, not a stale ID carried over from somewhere.
  if calls_for helm | grep -q "image.id=sha256"; then
    printf '\nexpected an empty image.id, got:\n%s\n' "$(calls_for helm)" >&2
    return 1
  fi
}
