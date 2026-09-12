#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# scenarios/_lib/workload.sh binds a hand-run scenario script to an app.
#
# Before it existed those scripts fell back to go-api when run from a terminal,
# so a scenario active on echo-server built, patched and traced go-api instead.

setup() {
  LIB="$BATS_TEST_DIRNAME/../../../scenarios/_lib/workload.sh"
  SCRIPT="$BATS_TEST_TMPDIR/tool.sh"
  printf '. "%s"\necho "app=$WORKLOAD_NAME ns=$WORKLOAD_NAMESPACE args=[$*] n=$#"\n' "$LIB" >"$SCRIPT"
  unset WORKLOAD_NAME WORKLOAD_NAMESPACE
}

@test "flags bind the app and are removed from the script's arguments" {
  run sh "$SCRIPT" v1.1.0 --app echo-server --namespace apps "two words"
  [ "$status" -eq 0 ]
  [ "$output" = "app=echo-server ns=apps args=[v1.1.0 two words] n=2" ]
}

@test "the --flag=value form works" {
  run sh "$SCRIPT" --app=java-api --namespace=java
  [ "$status" -eq 0 ]
  [ "$output" = "app=java-api ns=java args=[] n=0" ]
}

@test "the namespace defaults to the app name" {
  run sh "$SCRIPT" --app echo-server
  [ "$status" -eq 0 ]
  [ "$output" = "app=echo-server ns=echo-server args=[] n=0" ]
}

@test "the engine's environment binds a script run with no flags" {
  WORKLOAD_NAME=go-api WORKLOAD_NAMESPACE=apps run sh "$SCRIPT" check
  [ "$status" -eq 0 ]
  [ "$output" = "app=go-api ns=apps args=[check] n=1" ]
}

@test "an explicit --app does not inherit the engine's namespace" {
  WORKLOAD_NAME=go-api WORKLOAD_NAMESPACE=go-api run sh "$SCRIPT" --app echo-server
  [ "$status" -eq 0 ]
  [ "$output" = "app=echo-server ns=echo-server args=[] n=0" ]
}

@test "no binding at all is a usage error, never a guess" {
  run sh "$SCRIPT" v1.1.0
  [ "$status" -eq 2 ]
  [[ "$output" == *"needs to know which app"* ]]
  [[ "$output" != *"app="* ]]
}

@test "a flag without a value is a usage error" {
  run sh "$SCRIPT" --app
  [ "$status" -eq 2 ]
  [[ "$output" == *"--app needs a value"* ]]
}
