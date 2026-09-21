#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# One gosec policy, in src/.golangci.yml. A bare `gosec ./...` reads neither
# that file's rule exclusions nor the per-site //nolint:gosec justifications, so
# it re-reports every finding the project has already answered and the gate goes
# permanently red. These tests pin the two halves of that invariant.

setup() {
  ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../../.." && pwd)"
  MK="$ROOT/src/make/test.mk"
  CFG="$ROOT/src/.golangci.yml"
}

# sec_recipe — the sec target's recipe lines, without the target line itself.
sec_recipe() {
  awk '/^sec:/{f=1;next} f && /^[a-zA-Z_.-]+:/{f=0} f' "$MK"
}

@test "the sec target runs gosec through golangci-lint" {
  run sec_recipe
  [ "$status" -eq 0 ]
  [[ "$output" == *"golangci-lint run --enable-only=gosec"* ]]
}

@test "the sec target never invokes the gosec binary directly" {
  # The regression this guards: a bare invocation passes for whoever adds it and
  # then fails for everyone, because it carries none of the policy.
  run sec_recipe
  [ "$status" -eq 0 ]
  # gosec as a COMMAND, not as the --enable-only=gosec argument.
  [[ ! "$output" =~ (^|[[:space:]]|@)gosec[[:space:]] ]]
}

@test "gosec is enabled in the golangci-lint linter set" {
  run grep -cE '^[[:space:]]+- gosec$' "$CFG"
  [ "$status" -eq 0 ]
  [ "$output" -ge 1 ]
}

@test "every gosec rule exclusion names itself in a written reason" {
  run python3 "$ROOT/src/test/shell/helpers/gosec_excludes_justified.py" "$CFG"
  [ "$status" -eq 0 ]
}
