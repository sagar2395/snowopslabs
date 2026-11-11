# RFC 0002: Owner-aware run recovery

**Status:** Accepted design; phase 1 implemented, phase 2 planned
**Author:** @kimjooyoon
**Date:** 2026-10-25

## Summary

Each engine has an owner ID and a file lock. Starting another
engine must leave a live owner's runs alone. If an engine dies, keep interrupted
running work visible and let the operator explicitly release its lock key.

Ship this recovery change first. Add stored cancellation requests in a separate
phase. Recovery can land before the
[traffic operation migration](https://github.com/snowops-oss/snowopslabs/issues/13),
without claiming to fix the separate remote-cancellation problem.

## Motivation

Two [reproducers and their raw results](https://gist.github.com/kimjooyoon/209dba6b3ca78c1bfc44aa3f5222c677/0d6986f1dd05df1d26be24dc666cefca92321c97)
show different problems:

- Starting an unlocked status engine marks another live engine's run cancelled.
  Startup recovery currently cancels every queued or running row.
- A CLI cancel request marks a run cancelled before its execution returns.
  Another operation on the same key can then start while cleanup is still running.

Both tests use temporary SQLite and a fake toolchain, without a cluster. The
cancellation test holds an in-process cleanup open. It does not test Kubernetes
process cleanup. These observations remain separate from the proposed changes.

## Phase 1: Preserve live owners and recover interrupted work

### Register each engine

Support engines on one host, using local storage and one canonical database path
and owner directory. Keep `<database>.owners` and its referenced files intact.
Deleting a file can leave work running without a checkable owner. Remote
filesystems, hard-link aliases, and external replacement of owner files are
unsupported.

Each engine creates a fresh random owner ID and an owner file beside the database.
It takes an exclusive nonblocking `flock` before accepting work. Keep that file
open and locked until all workers have returned and saved their results. If
shutdown times out, keep the lock and store open while workers finish.

Do not pass the descriptor to child processes. Never reuse an owner ID, replace a
referenced file, or create a missing file during a probe. A busy lock means another
engine may still own the run. Acquiring its lock allows recovery of that owner's
rows. It does not prove that child processes have stopped. A missing file does
not prove the owner exited: a process can retain its lock after deletion.

After taking its own lock at startup, remove old owner files only if no unfinished
or recovery-required run names them and their lock can be acquired. Delete each
file while holding its lock. Leave referenced and busy files intact. Fresh random
owner IDs prevent a later engine from reusing a deleted file's name.
Skip problematic unused entries, continue cleaning other files, and log the errors
without blocking engine startup. Cancellation still stops startup.

These are cooperation locks, not authentication. Use a cgo-free adapter on macOS
and Linux, including WSL2 in its Linux home. The
[macOS](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/flock.2.html)
and [Linux](https://man7.org/linux/man-pages/man2/flock.2.html) manuals describe the
nonblocking operation and how duplicated descriptors share a lock.

### Record ownership and recovery

Add these internal store fields in phase 1:

| Field | Meaning |
|---|---|
| `owner_id` | Engine identity; empty only for migrated legacy rows |
| `recovery_state` | `none`, `required`, or `released` |
| `recovery_reason` | Why recovery was required or the key was released |
| `released_at` | Time of an explicit operator release; empty otherwise |
| `released_by` | Local operator UID and available user name; empty otherwise |

Keep the existing lifecycle statuses and atomic `CreateRun` insertion. Queued and
running rows still reserve their lock keys. With recovery required, that status
records the last saved phase; it does not say the operation is currently running.

Bind new runs to their submitting engine. Worker start and completion writes must
match its owner ID and `recovery_state=none`. Recovery rechecks the owner, status,
and recovery state in the same transaction that changes the row. It holds the
owner file lock through that transaction. Do not replace atomic insertion with a
separate conflict check or a process-local mutex.

The existing remote-cancel path remains unchanged in phase 1. It can still mark a
run cancelled before cleanup returns. The owner guards above do not fix that
path; phase 2 does.

### Recover at startup

Inspect only owners of active rows. A successful owner-lock probe applies only
to that owner's rows.

| Owner check | Last saved phase | Result |
|---|---|---|
| Lock busy | queued or running | Leave the row, key, and logs unchanged |
| Existing lock acquired | queued | Cancel without executing; release the key |
| Existing lock acquired | running | Mark recovery required; keep the key and logs |
| Legacy row during offline upgrade | queued or running | Mark recovery required; keep the key and logs |
| Missing file | queued | Cancel without executing; release the key; continue startup |
| Missing file | running | Mark recovery required with reason `owner_missing`; keep the key and logs; continue startup |
| Other failed probe | queued or running | Report the error; leave the row and owner's write access unchanged |
| Finished row | any | Leave unchanged |

Save the transition to running before any script or in-process action starts.
This makes recovery of a dead owner's queued work safe. A crash just after that
transition still needs recovery, even if no script started. Repeated startup
recovery must give the same result. A missing owner blocks only that owner's
running work; other owners and other keys can proceed. A cancelled queued row
cannot start because worker start requires that it is still queued.

### Release a key explicitly

**CLI:** `labctl runs recover <id> --force` releases a run whose recovery
state is `required`. Without `--force`, show the run, its key, the warning below,
and how to retry. Do not change the row.

Warn before a forced release: "The operation may still be running. Releasing this
key allows another operation on the same key. Check the lab before continuing."
This is an operator override, not a check that execution or cleanup finished.

For a run with an existing owner file, acquire that owner's lock and hold it
through the store transaction. Refuse a busy lock even with `--force`. For an `owner_missing`
run, allow explicit release without recreating the file, and add this warning:
"labctl can't check whether this work is still running. Check the lab before
releasing it." Keep `owner_missing` as the audit reason. Other failed probes keep
the key and report the error. Migrated rows without an owner ID can use the same
command after the offline upgrade. They have no owner file to probe.

In one transaction, recheck that recovery is still required, mark the run
cancelled, set `recovery_state=released`, and record the release time and local
operator UID and available user name. Set `recovery_reason=forced_release` for
ordinary interrupted work; retain `owner_missing` for a missing-owner release.
Keep the run ID, former owner ID, and partial logs. If the audit fields cannot be saved, do not release
the key. Two concurrent recoveries must not release or record the same run twice.
A repeated command reports the existing release without replacing its audit data.

The command reports: "The key was released. The operation may still be running."
It does not signal a saved PID or process-group ID. Released rows cannot later be
changed by an old owner's completion write. A surviving child can still affect
the lab; the warning and retained audit record make that limitation explicit.

### Show recovery in clients

Add optional recovery and release fields to run summaries, details, and log
responses. Display `Recovery required` with the recovery command for a
held key. After a forced release, display `Key released by operator`, including
who released it, when, and the warning that work may still be running.

A follower drains saved logs, then returns a recovery-required error instead of
waiting forever. Keep the log API's existing completion signal unchanged. Do not
show recovery-required work as an advancing install or offer ordinary cancellation
as a way to release its key. Clients that ignore the new fields may show only the
old lifecycle status. Document that limitation when implementing the API changes.

### Upgrade offline

Stop every old labctl process, including servers and workers. Upgrade all local
entry points, back up the database, then migrate before restarting. Mixed-version
and online upgrades are unsupported.

New engines take a shared upgrade lock before opening the store and keep it for
the store's lifetime. Migration tries that lock exclusively without waiting and
refuses if it is busy. It also probes the locks for recorded nonempty owner IDs
and refuses busy, unreadable, or invalid files. A missing file does not block
migration: the exclusive upgrade lock already excludes open stores and retained
workers. Preserve missing-owner runs, their recovery state, keys and logs.
Ownerless legacy rows have no file to probe. Keep the acquired locks until the migration transaction finishes.
Active legacy rows become recovery required and can then use the explicit recovery
command. Do not discard their keys or logs during migration.

The first upgrade cannot detect old processes that never used these locks.
Stopping those processes is an operator prerequisite, not something a new schema
field or lock can prove. This proposal does not support leaving an old binary
running against the upgraded database. The migration guide must state that limit.

### Acceptance for phase 1

Implement and test this phase independently of stored cancellation requests:

1. Starting a second engine preserves a live owner's run and key. A paused owner
   is still live; a failed probe must not change its row or block its own writes.
2. A dead owner's queued work is cancelled without execution. Interrupted running
   work keeps its key and logs, including when a child process survives. Missing
   owners cancel queued work and mark running work `required` / `owner_missing`;
   startup succeeds and another owner or key still works.
3. Recovery without `--force` changes nothing. A forced release records its actor,
   time, reason, and former owner, and allows a new same-key run. It does not claim
   that the surviving child stopped. An `owner_missing` release adds the stronger
   warning and retains that audit reason. Busy-owner and other failed-probe cases
   retain the key.
4. Concurrent and repeated recovery records one release. A failed audit write keeps
   the key. An old completion cannot overwrite the released result.
5. Separate connections competing for the same key have one holder. Disjoint keys
   and unlocked probes still proceed.
6. Shutdown timeouts retain the store and owner lock until workers finish. Children
   do not inherit the descriptor. Startup removes only unlocked owner files that
   no unfinished or recovery-required run names, and holds each lock while
   deleting its file. Referenced or busy owner files are never removed.
7. Migration allows missing owner files under the exclusive upgrade lock and
   refuses busy files and other failed checks. Missing-owner runs keep their
   recovery state, keys and logs and can still be explicitly released.
   Migrated ownerless rows keep
   their logs and have a supported explicit release path after the offline upgrade.
8. CLI, API, and follower tests show both recovery required and operator release.
   The remote-cancellation reproducer remains a known limitation until phase 2.

Use local subprocesses for file-lock and surviving-child cases on macOS and Linux.
A local pass on one platform does not establish the result on the other; keep
both native checks in the implementation's validation evidence.

## Phase 2: Store cancellation requests

**Planned separately:** add `cancel_requested_at` and change all cancellation entry
points to save an idempotent request. A running operation keeps its status, owner,
and key while its owner cancels the context and waits for execution to return.
Only then does normal completion release that key. The explicit operator recovery
command remains the separate override for interrupted work.

A guarded queued-to-running transition checks for a pending request. The owner
acknowledges a queued cancellation without launching work. The CLI says
`Cancellation requested` until completion is saved. These fields, pickup checks,
and wording are not part of phase 1.

Phase 2 must make the remote-cancellation reproducer pass. Test requests arriving
at pickup, concurrent cancellation and normal exit, and retained keys during
cleanup. Choose the polling interval in that implementation. Do not delay phase 1
or describe its recovery change as satisfying these cancellation tests.

## Alternatives and future work

| Choice | Reason |
|---|---|
| Cancel every active row at startup | Cancels work owned by a live engine |
| Hold one exclusive lock for every engine | Prevents independent operations from coexisting |
| Use a PID or elapsed heartbeat alone | A reused PID or paused owner can look like an exited owner |
| Keep interrupted keys forever | Leaves a local lab stuck without a supported recovery command |
| Release interrupted keys automatically | The owner may have left a child process running |
| Store cancellation requests in phase 1 | Makes the recovery fix harder to review and ship on its own |

A later supervisor could establish when surviving execution has stopped and allow
automatic release. That is future work, not a condition for accepting phase 1 or
using explicit operator recovery. No daemon or remote service is required here.

Owner files add one small file per engine. Retention may remove a file only after
acquiring its lock and confirming that no row references its owner ID. Never reuse
that ID. During implementation, update the architecture, locking ADR, CLI/API
reference, and cancellation runbook in the same change. In particular, explain
when recovery holds a key and when the operator may release it.
