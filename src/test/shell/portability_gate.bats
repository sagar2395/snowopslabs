#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# portability-gate: ignore-file — this file contains the GNU-only constructs it
# asserts against, as test data.
#
# The portability gate is the automated half of golden rule 1. These tests
# assert it catches each GNU-only construct, and — just as important — that it
# does not fire on comments, so documenting a pitfall is not itself a violation.

setup() {
  ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  GATE="$ROOT/scripts/lint-portability.sh"
  WORK="$(mktemp -d "${TMPDIR:-/tmp}/portability.XXXXXX")"
}

teardown() {
  rm -rf "$WORK"
}

# scan <file-contents> — write a script into an isolated tree and run the gate
# over it. The gate always scans from its own repository root, so we copy it
# into a scratch repo to keep the assertions hermetic.
scan() {
  mkdir -p "$WORK/repo/scripts"
  cp "$GATE" "$WORK/repo/scripts/lint-portability.sh"
  printf '%s\n' "$1" >"$WORK/repo/candidate.sh"
  run bash "$WORK/repo/scripts/lint-portability.sh"
}

@test "accepts a portable script" {
  scan '#!/usr/bin/env bash
set -euo pipefail
value=$(grep -E "pattern" file.txt)
dir=$(cd "$(dirname "$0")" && pwd)
echo "$value $dir"'
  [ "$status" -eq 0 ]
  [[ "$output" == *"portability gate passed"* ]]
}

@test "rejects grep -P" {
  scan 'grep -P "\\d+" file.txt'
  [ "$status" -ne 0 ]
  [[ "$output" == *"GNU-only"* ]]
  [[ "$output" == *"candidate.sh"* ]]
}

@test "rejects sed -i without a backup suffix but allows sed -i.bak" {
  scan 'sed -i "s/a/b/" file.txt'
  [ "$status" -ne 0 ]

  scan 'sed -i.bak "s/a/b/" file.txt && rm -f file.txt.bak'
  [ "$status" -eq 0 ]
}

@test "rejects readlink -f" {
  scan 'target=$(readlink -f "$0")'
  [ "$status" -ne 0 ]
  [[ "$output" == *"readlink -f"* ]]
}

@test "rejects date -d" {
  scan 'when=$(date -d "yesterday" +%s)'
  [ "$status" -ne 0 ]
}

@test "rejects stat -c" {
  scan 'size=$(stat -c %s file.txt)'
  [ "$status" -ne 0 ]
}

@test "rejects base64 -w" {
  scan 'encoded=$(base64 -w0 <file.bin)'
  [ "$status" -ne 0 ]
}

@test "rejects xargs -r" {
  scan 'kubectl get crd -o name | xargs -r kubectl delete'
  [ "$status" -ne 0 ]
}

@test "rejects hardcoded linux/amd64 in a shell script" {
  scan 'docker build --platform linux/amd64 .'
  [ "$status" -ne 0 ]
  [[ "$output" == *"Apple Silicon"* ]]
}

@test "does not fire on a comment describing the pitfall" {
  scan '# Avoid readlink -f and sed -i here: both are GNU-only.
  # Use grep -P nowhere.
dir=$(cd "$(dirname "$0")" && pwd)
echo "$dir"'
  [ "$status" -eq 0 ]
}

@test "reports the file and line number of a violation" {
  scan 'echo one
echo two
readlink -f /tmp'
  [ "$status" -ne 0 ]
  [[ "$output" == *":3:"* ]]
}

@test "reports every distinct violation, not just the first" {
  scan 'readlink -f /tmp
grep -P "x" f'
  [ "$status" -ne 0 ]
  [[ "$output" == *"readlink -f"* ]]
  [[ "$output" == *"PCRE"* ]]
}

@test "honours the ignore-file marker" {
  scan '# portability-gate: ignore-file
readlink -f /tmp'
  [ "$status" -eq 0 ]
}

@test "the repository itself passes the gate" {
  run bash "$GATE"
  [ "$status" -eq 0 ]
}
