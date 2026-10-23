#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# backup-restore-drill's backup.sh captures a namespace into an archive that
# re-applies cleanly. Objects that Kubernetes recreates in every namespace stay
# out of it: restoring them only adds noise.

load 'helpers/stub'

setup() {
  stub_setup
  command -v jq >/dev/null 2>&1 || {
    echo "these tests need jq, which backup.sh itself requires" >&2
    return 1
  }
  ROOT="$(project_root)"
  SCRIPT="$ROOT/scenarios/backup-restore-drill/scripts/backup.sh"
  export BACKUP_DIR="$STUB_DIR/backups"
  ARCHIVE="$BACKUP_DIR/shop-latest.json"
  # A namespace as the cluster lists it: the CA bundle and the default
  # ServiceAccount that Kubernetes adds, next to the learner's objects.
  stub_script kubectl <<'STUB'
case "$*" in
  "get namespace shop") ;;
  "get "*" -n shop -o json")
    cat <<'JSON'
{"kind":"List","items":[
  {"kind":"ConfigMap","metadata":{"name":"kube-root-ca.crt","namespace":"shop"},"data":{"ca.crt":"cert"}},
  {"kind":"ConfigMap","metadata":{"name":"restore-marker","namespace":"shop"},"data":{"token":"tok"}},
  {"kind":"ServiceAccount","metadata":{"name":"default","namespace":"shop"}},
  {"kind":"Deployment","metadata":{"name":"data-writer","namespace":"shop"}}
]}
JSON
    ;;
  *) exit 1 ;;
esac
STUB
}

teardown() { stub_teardown; }

# names - the kind/name of each object in the archive, one per line.
names() {
  jq -r '.items[] | "\(.kind)/\(.metadata.name)"' "$ARCHIVE"
}

@test "the archive leaves out kube-root-ca.crt, which Kubernetes recreates in every namespace" {
  run bash "$SCRIPT" shop
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  run names
  [[ "$output" != *"ConfigMap/kube-root-ca.crt"* ]] || { echo "$output"; return 1; }
}

@test "the archive keeps the learner's ConfigMaps and the rest of the namespace" {
  run bash "$SCRIPT" shop
  [ "$status" -eq 0 ] || { echo "$output"; return 1; }
  [[ "$output" == *"Backed up 2 object(s)"* ]] || { echo "$output"; return 1; }
  run names
  [ "$output" = "ConfigMap/restore-marker
Deployment/data-writer" ] || { echo "$output"; return 1; }
}
