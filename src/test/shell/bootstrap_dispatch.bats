#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# bootstrap/setup-tools.sh profile dispatch (W3-T07). The tool *downloads* are
# not exercised here (they need network + sudo); these cover the branching that
# decides what to do before any download — the help path and the unknown-profile
# guard — so a typo in a profile name fails loudly instead of silently doing the
# wrong thing. The pure helpers (version_ge, is_wsl) are covered in
# setup_tools_versions.bats and setup_tools_wsl.bats.

setup() {
  ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../../.." && pwd)"
  SCRIPT="$ROOT/bootstrap/setup-tools.sh"
}

@test "help prints usage and exits 0 without installing anything" {
  run bash "$SCRIPT" help
  [ "$status" -eq 0 ]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"Profiles:"* ]]
}

@test "--help is accepted as an alias" {
  run bash "$SCRIPT" --help
  [ "$status" -eq 0 ]
  [[ "$output" == *"Usage:"* ]]
}

@test "an unknown profile fails loudly with a non-zero exit" {
  run bash "$SCRIPT" not-a-real-profile
  [ "$status" -ne 0 ]
  [[ "$output" == *"unknown profile"* ]]
  # It still shows usage so the user can see the valid choices.
  [[ "$output" == *"Profiles:"* ]]
}

@test "the script reports the detected OS/ARCH in its help banner" {
  run bash "$SCRIPT" help
  [ "$status" -eq 0 ]
  [[ "$output" == *"Detected:"* ]]
}
