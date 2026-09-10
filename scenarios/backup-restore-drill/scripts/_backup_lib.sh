#!/usr/bin/env bash
# Shared paths and helpers for the backup/restore drill.
#
# Sourced by the drill's own tools (backup.sh, restore.sh) and by its checks, so
# that "where does the archive live" and "what is the marker's token" are
# answered in exactly one place. A check that guessed differently from the tool
# would grade a file the learner never wrote.

# The namespace the drill operates on. Every caller takes it as $1 for the
# learner's benefit ("back up THIS namespace"), and falls back to the bound
# workload's namespace when run by the check runner, which passes no arguments.
backup_ns() {
  echo "${1:-${WORKLOAD_NAMESPACE:-${WORKLOAD_NAME:-go-api}}}"
}

# Archives and drill state live under .labctl (gitignored runtime state).
# PROJECT_ROOT is exported by labctl; a bare relative path would resolve against
# whatever directory the learner happened to be in.
backup_dir() {
  echo "${BACKUP_DIR:-${PROJECT_ROOT:-.}/.labctl/backups}"
}

archive_path() { echo "$(backup_dir)/$(backup_ns "${1:-}")-latest.json"; }

# One line per backup: "<timestamp> <boot-id>". The FIRST line is the fingerprint
# of the data as it was when the drill began, which is what the PV-data lesson is
# graded against.
bootid_log() { echo "$(backup_dir)/$(backup_ns "${1:-}")-bootid.log"; }

# One line per verify that found the restore marker missing. This is how the
# drill knows the learner actually caused a loss, rather than never touching
# anything: live state alone cannot tell "restored" from "never broken".
loss_log() { echo "$(backup_dir)/$(backup_ns "${1:-}")-loss.log"; }

require_jq() {
  command -v jq >/dev/null 2>&1 && return 0
  echo "ERROR: 'jq' is required to read and scrub Kubernetes manifests, but was not found." >&2
  echo "       Install it: brew install jq  (macOS) | apt-get install jq  (Debian/Ubuntu)" >&2
  echo "       'labctl doctor' reports it alongside the rest of the toolchain." >&2
  return 1
}

# The boot-id the data-writer persisted on its volume, or empty if it cannot be
# read. Never fails the caller: "no pod" and "no file yet" are states the drill
# passes through legitimately.
live_boot_id() {
  ns="$(backup_ns "${1:-}")"
  pod="$(kubectl -n "$ns" get pod -l app=data-writer \
    -o jsonpath='{.items[?(@.status.phase=="Running")].metadata.name}' 2>/dev/null | awk '{print $1}')"
  [ -n "$pod" ] || return 0
  kubectl -n "$ns" exec "$pod" -- sh -c 'cat /data/boot-id 2>/dev/null' 2>/dev/null || true
}
