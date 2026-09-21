#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# `traffic status` reports state from a single snapshot of the job.
#
# Assembling the report from several reads let it contradict itself: the job
# carries ttlSecondsAfterFinished, so it can be deleted between two calls, and
# the status then announced "running" and immediately failed with a raw
# NotFound and a bare "exit status 1" — observed on a live lab.

load 'helpers/stub'

setup() {
  stub_setup
  stub_command kubectl
  STATUS="$(project_root)/src/services/traffic/status.sh"
}

teardown() {
  stub_teardown
}

@test "no namespace reports not running, and succeeds" {
  stub_when kubectl "get namespace" 1 ""

  run bash "$STATUS"
  [ "$status" -eq 0 ]
  [[ "$output" == *"not running"* ]]
}

@test "a namespace with no job reports not running rather than erroring" {
  stub_when kubectl "get namespace" 0 ""
  stub_when kubectl "get job traffic-k6" 1 ""

  run bash "$STATUS"
  [ "$status" -eq 0 ]
  [[ "$output" == *"not running"* ]]
  [[ "$output" != *"NotFound"* ]]
}

@test "a live job reports running with its profile" {
  stub_when kubectl "get namespace" 0 ""
  stub_when kubectl "-o json" 0 '{"metadata":{"labels":{"traffic-profile":"steady"}},"status":{"active":1}}'

  run bash "$STATUS"
  [ "$status" -eq 0 ]
  [[ "$output" == *"running (profile: steady)"* ]]
}

@test "a finished job reports completed, not running" {
  stub_when kubectl "get namespace" 0 ""
  stub_when kubectl "-o json" 0 '{"metadata":{"labels":{"traffic-profile":"spike"}},"status":{"succeeded":1}}'

  run bash "$STATUS"
  [ "$status" -eq 0 ]
  [[ "$output" == *"completed"* ]]
  [[ "$output" != *"Traffic generator: running"* ]]
}
