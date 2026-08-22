#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# Guards the version-comparison helper in bootstrap/setup-tools.sh.
#
# The bug this file exists to prevent: a user brought helm v4.2.3 (a valid
# newer version), the installer treated the pinned HELM_VERSION as an exact
# match, and setup exited non-zero on a healthy machine. The whole toolchain
# is minimum-versioned; the installer must be too.

setup() {
  ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  # Extract only the helper function so we can source it without executing
  # the installer's entry point or its ANSI colour setup.
  helpers="$(mktemp "${TMPDIR:-/tmp}/version_ge.XXXXXX.sh")"
  sed -n '/^version_ge()/,/^}$/p' "$ROOT/bootstrap/setup-tools.sh" >"$helpers"
  # shellcheck source=/dev/null
  source "$helpers"
  rm -f "$helpers"
}

@test "version_ge accepts an equal version" {
  version_ge "3.14.0" "3.14.0"
}

@test "version_ge accepts a newer minor" {
  version_ge "3.15.0" "3.14.0"
}

@test "version_ge accepts a newer major (helm 4 satisfies the 3.12 minimum)" {
  # The user's actual failure: helm v4.2.3 must clear a 3.14.0 minimum.
  version_ge "4.2.3" "3.14.0"
  version_ge "4.2.3" "3.12.0"
}

@test "version_ge rejects an older version" {
  ! version_ge "3.9.0" "3.14.0"
}

@test "version_ge rejects an older patch" {
  ! version_ge "3.14.0" "3.14.1"
}

@test "version_ge handles two-part versions on either side" {
  version_ge "1.29" "1.28.0"
  ! version_ge "1.27" "1.28.0"
}

@test "version_ge treats a missing 'have' as failing rather than passing" {
  # An empty 'have' means we could not detect the tool. Assuming success would
  # skip the install and let a broken environment slip through.
  ! version_ge "" "3.14.0"
}

@test "version_ge with an empty 'want' accepts anything" {
  # No minimum stated means any version is fine — matches Requirement's
  # MinVersion="" semantic in internal/toolchain/preflight.go.
  version_ge "0.0.1" ""
}

@test "version_ge handles a v-prefix consistently" {
  # sort -V accepts leading 'v' on modern coreutils and BSD, so a tool that
  # reports "v3.14.0" via one path and "3.14.0" via another still compares.
  version_ge "v4.2.3" "3.14.0"
  version_ge "4.2.3" "v3.14.0"
}
