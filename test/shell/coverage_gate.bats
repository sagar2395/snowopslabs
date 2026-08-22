#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# The coverage gate decides whether a change may merge, so its own logic needs
# to be right: exact per-package arithmetic, a ratchet that cannot be loosened,
# and no way for a stale entry to hide an untested package.

load 'helpers/stub'

setup() {
  ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  GATE="$ROOT/scripts/coverage-gate.sh"
  WORK="$(mktemp -d "${TMPDIR:-/tmp}/coverage-gate.XXXXXX")"
  PROFILE="$WORK/coverage.out"
  EXCEPTIONS="$WORK/exceptions"
  : >"$EXCEPTIONS"
}

teardown() {
  rm -rf "$WORK"
}

# write_profile <pkg> <covered-stmts> <uncovered-stmts>
# Emits a profile fragment: one covered block and one uncovered block.
write_profile() {
  local pkg="$1" covered="$2" uncovered="$3"
  if [ ! -s "$PROFILE" ]; then echo "mode: atomic" >"$PROFILE"; fi
  [ "$covered" -gt 0 ] && echo "$pkg/a.go:1.1,2.1 $covered 1" >>"$PROFILE"
  [ "$uncovered" -gt 0 ] && echo "$pkg/b.go:1.1,2.1 $uncovered 0" >>"$PROFILE"
  return 0
}

run_gate() {
  COVERAGE_EXCEPTIONS="$EXCEPTIONS" run bash "$GATE" "$PROFILE" "${1:-80}"
}

@test "passes when every package clears the minimum" {
  write_profile "example.com/m/pkga" 9 1   # 90%
  write_profile "example.com/m/pkgb" 8 2   # 80% — exactly on the line
  run_gate
  [ "$status" -eq 0 ]
  [[ "$output" == *"coverage gate passed"* ]]
}

@test "fails a package below the minimum and names it with its percentage" {
  write_profile "example.com/m/pkga" 5 5   # 50%
  run_gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"pkga"* ]]
  [[ "$output" == *"50.0%"* ]]
}

@test "computes package coverage across files, not per-file averages" {
  # 1 of 1 covered in a.go plus 0 of 99 in b.go is 1%, not the 50% a per-file
  # average would report. This is the bug the exact aggregation avoids.
  echo "mode: atomic" >"$PROFILE"
  echo "example.com/m/pkga/a.go:1.1,2.1 1 1" >>"$PROFILE"
  echo "example.com/m/pkga/b.go:1.1,2.1 99 0" >>"$PROFILE"
  run_gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"1.0%"* ]]
}

@test "an exception lets a listed package stay below the global minimum" {
  write_profile "example.com/m/legacy" 5 5   # 50%
  echo "example.com/m/legacy 50 W3 scheduled for rewrite" >"$EXCEPTIONS"
  run_gate
  [ "$status" -eq 0 ]
}

@test "an exception is a floor: regression below it fails" {
  write_profile "example.com/m/legacy" 4 6   # 40%
  echo "example.com/m/legacy 50 W3 scheduled for rewrite" >"$EXCEPTIONS"
  run_gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"exception floor"* ]]
}

@test "an exception that is no longer needed must be removed" {
  write_profile "example.com/m/legacy" 9 1   # 90% — clears the global bar
  echo "example.com/m/legacy 50 W3 scheduled for rewrite" >"$EXCEPTIONS"
  run_gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"RATCHET"* ]]
  [[ "$output" == *"remove its .coverage-exceptions entry"* ]]
}

@test "a stale exception for a package with no coverage fails" {
  write_profile "example.com/m/pkga" 9 1
  echo "example.com/m/deleted-package 50 W3 gone" >"$EXCEPTIONS"
  run_gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"STALE"* ]]
}

@test "comments and blank lines in the exceptions file are ignored" {
  write_profile "example.com/m/legacy" 5 5
  printf '# a comment\n\n   \nexample.com/m/legacy 50 W3 rewrite\n' >"$EXCEPTIONS"
  run_gate
  [ "$status" -eq 0 ]
}

@test "packages with zero statements are skipped rather than failed" {
  echo "mode: atomic" >"$PROFILE"
  echo "example.com/m/typesonly/types.go:1.1,2.1 0 0" >>"$PROFILE"
  write_profile "example.com/m/pkga" 9 1
  run_gate
  [ "$status" -eq 0 ]
}

@test "a missing profile is an actionable error, not a silent pass" {
  COVERAGE_EXCEPTIONS="$EXCEPTIONS" run bash "$GATE" "$WORK/absent.out" 80
  [ "$status" -ne 0 ]
  [[ "$output" == *"not found"* ]]
  [[ "$output" == *"make test-go"* ]]
}

@test "the minimum is configurable" {
  write_profile "example.com/m/pkga" 6 4   # 60%
  run_gate 50
  [ "$status" -eq 0 ]
  run_gate 70
  [ "$status" -ne 0 ]
}

@test "the repository's own exceptions file is well-formed" {
  # Every entry must carry a minimum, a wave and a reason — an exception
  # without a stated expiry is how a temporary gap becomes permanent.
  run awk '
    /^[[:space:]]*#/ { next }
    NF == 0 { next }
    NF < 4 { print "malformed: " $0; bad = 1 }
    $2 !~ /^[0-9]+(\.[0-9]+)?$/ { print "not a percentage: " $0; bad = 1 }
    $3 !~ /^W[0-9]+$/ { print "not a wave id: " $0; bad = 1 }
    END { exit bad ? 1 : 0 }
  ' "$ROOT/.coverage-exceptions"
  [ "$status" -eq 0 ]
}
