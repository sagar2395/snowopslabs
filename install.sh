#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Install the labctl binary that matches this clone of SnowOps Labs, and record
# the clone so labctl finds it from any directory.
#
#   git clone --branch stable https://github.com/snowops-oss/snowopslabs.git
#   cd snowopslabs && ./install.sh
#
# The version comes from the clone's LAB_VERSION. After upgrading the clone
# (git merge origin/stable), run it again to bring labctl in step.
# Runs on macOS, Linux and Windows under WSL2, and needs no sudo.
#
# Environment:
#   SNOWOPS_VERSION      labctl version to install instead of LAB_VERSION
#   SNOWOPS_BIN_DIR      where labctl goes (default ~/.local/bin)
#   SNOWOPS_HOME         labctl's state directory (default ~/.snowops)
#   SNOWOPS_RELEASE_URL  releases base URL (default: the GitHub releases page)

set -eu

REPO="snowops-oss/snowopslabs"
BIN_DIR="${SNOWOPS_BIN_DIR:-$HOME/.local/bin}"
STATE_HOME="${SNOWOPS_HOME:-$HOME/.snowops}"
RELEASE_URL="${SNOWOPS_RELEASE_URL:-https://github.com/$REPO/releases}"

say() { printf '%s\n' "$*"; }
fail() {
  printf 'install.sh: %s\n' "$*" >&2
  exit 1
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    MINGW* | MSYS* | CYGWIN*) fail "run this inside WSL2 (Ubuntu), not in Windows directly" ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *) fail "unsupported CPU architecture: $(uname -m)" ;;
  esac
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 "$1" | cut -d ' ' -f 1
  fi
}

# lab_version is the release this clone belongs to, or the SNOWOPS_VERSION
# override. Development content (main) has no labctl release to download.
lab_version() {
  if [ -n "${SNOWOPS_VERSION:-}" ]; then
    echo "${SNOWOPS_VERSION#v}"
    return
  fi
  [ -f "$LAB_DIR/LAB_VERSION" ] || fail "$LAB_DIR has no LAB_VERSION; clone a release with: git clone --branch stable https://github.com/$REPO.git"
  version="$(tr -d '[:space:]' <"$LAB_DIR/LAB_VERSION")"
  case "$version" in
    *-*) fail "this checkout is development content (LAB_VERSION=$version). Switch to a release with 'git switch stable', or build labctl from source (see CONTRIBUTING.md)." ;;
  esac
  echo "$version"
}

# Tell the user how to put $BIN_DIR on their PATH with one command they can
# paste. The login shell decides which startup file the PATH line belongs in:
# zsh reads ~/.zshrc, bash on macOS reads ~/.bash_profile, bash on Linux reads
# ~/.bashrc. The printed command creates that file when it is missing, so a
# fresh Mac with no ~/.zshrc does not need one hand-written first.
path_hint() {
  rc_file=""
  case "${SHELL:-}" in
    */zsh) rc_file="$HOME/.zshrc" ;;
    */bash)
      if [ "$os" = darwin ]; then
        rc_file="$HOME/.bash_profile"
      else
        rc_file="$HOME/.bashrc"
      fi
      ;;
  esac

  # Keep $HOME in the printed line when labctl goes to the default place, so
  # the line stays right if the home directory ever moves. A custom
  # SNOWOPS_BIN_DIR is printed as the absolute path it is.
  if [ "$BIN_DIR" = "$HOME/.local/bin" ]; then
    path_literal='$HOME/.local/bin'
  else
    path_literal="$BIN_DIR"
  fi

  say ""
  if [ -z "$rc_file" ]; then
    say "$BIN_DIR is not on your PATH. Add this line to your shell's startup file, then open a new terminal:"
    say "  export PATH=\"$BIN_DIR:\$PATH\""
    return
  fi

  say "$BIN_DIR is not on your PATH. Run this to add it (it creates $rc_file if it is missing):"
  say "  echo 'export PATH=\"$path_literal:\$PATH\"' >> \"$rc_file\" && source \"$rc_file\""
}

main() {
  command -v curl >/dev/null 2>&1 || fail "curl is required"
  command -v tar >/dev/null 2>&1 || fail "tar is required"

  LAB_DIR="$(cd "$(dirname "$0")" && pwd)"
  [ -d "$LAB_DIR/scenarios" ] && [ -d "$LAB_DIR/runtimes" ] ||
    fail "run install.sh from your snowopslabs clone: cd snowopslabs && ./install.sh"

  os="$(detect_os)"
  arch="$(detect_arch)"
  version="$(lab_version)"
  archive="labctl_${version}_${os}_${arch}.tar.gz"
  base="$RELEASE_URL/download/v$version"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT INT TERM

  say "Downloading labctl $version for $os/$arch..."
  curl -fsSL "$base/$archive" -o "$tmp/$archive" || fail "could not download $base/$archive"
  curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || fail "could not download the checksums"

  expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
  [ -n "$expected" ] || fail "$archive is not listed in checksums.txt"
  [ "$(sha256 "$tmp/$archive")" = "$expected" ] || fail "checksum mismatch for $archive; the download is corrupt"

  mkdir -p "$tmp/x"
  tar -xzf "$tmp/$archive" -C "$tmp/x"
  [ -f "$tmp/x/labctl" ] || fail "$archive has no labctl binary"

  mkdir -p "$BIN_DIR"
  # mv replaces the file rather than rewriting it in place, which macOS would
  # treat as a tampered signed binary and kill on its next run.
  mv "$tmp/x/labctl" "$BIN_DIR/labctl"
  chmod 755 "$BIN_DIR/labctl"

  mkdir -p "$STATE_HOME"
  printf '%s\n' "$LAB_DIR" >"$STATE_HOME/lab-dir"
  if [ ! -f "$LAB_DIR/.env" ] && [ -f "$LAB_DIR/config/.env.example" ]; then
    cp "$LAB_DIR/config/.env.example" "$LAB_DIR/.env"
  fi

  say ""
  say "Installed labctl $version to $BIN_DIR/labctl."
  say "Your lab is $LAB_DIR; labctl uses it from any directory."
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *)
      path_hint
      ;;
  esac
  say ""
  say "Next:"
  say "  labctl init    # build the lab: tools, Docker, cluster, platform (about 5-10 minutes)"
  say "  labctl ui      # open the dashboard at http://localhost:3939"
}

main "$@"
