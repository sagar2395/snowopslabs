# R01 — Run Engine & Cancellation

**Wave:** W1 · **Time:** ~30 minutes · **Cluster needed:** no (a real cluster makes step 5 more convincing)

This runbook proves the four properties Wave 1 exists for. Each was broken in
v1, and each is the kind of thing that only really convinces on real hardware:

1. A cancelled run dies, **and takes its children with it**.
2. Killing the process mid-run leaves a coherent record, not a phantom.
3. Conflicting operations are refused immediately, naming the holder.
4. No output is ever lost, including across a disconnect.

---

## Preconditions

- `bin/labctl` built (`make cli-build`).
- A scratch `SNOWOPS_HOME` so this runbook never touches your real history:

```bash
$ export SNOWOPS_HOME=/tmp/snowops-r01
$ mkdir -p "$SNOWOPS_HOME"
```

- `pgrep` and `pkill` available (both ship with macOS and Linux).

**Teardown is step 8.** Nothing here touches a cluster unless you choose the
optional variant in step 5.

---

## 1. 🔍 The database is created where you expect

```bash
$ ./bin/labctl runs list
```

**Expect:** `No runs recorded yet.`

```bash
$ ls -la "$SNOWOPS_HOME"
```

**Expect:** `snowops.db` plus WAL sidecar files (`-wal`, `-shm`). Those
sidecars are how a reader proceeds while a write is in flight.

**Failure signature — a permissions error:** `SNOWOPS_HOME` points somewhere
unwritable. Pick another directory.

---

## 2. The automated cancellation suite passes on your machine

The process-group behaviour is OS-specific — signal delivery differs between
macOS and Linux — so it must be exercised where you actually work.

```bash
$ go test -race -run 'TestExecCancellation' ./internal/toolchain/ -v
```

**Expect:** all subtests `PASS`, notably:

```
--- PASS: TestExecCancellation/kills_the_whole_process_group,_not_just_the_direct_child
--- PASS: TestExecCancellation/SIGKILLs_a_process_that_ignores_SIGTERM
```

That second one matters: a script that traps `SIGTERM` must still die after the
grace period. A tool that can be made unkillable by a badly-written script is
not one you can trust with a cluster.

```bash
$ go test -race ./internal/run/ ./internal/store/
```

**Expect:** both `ok`.

---

## 3. ⚠️ Cancel a long-running operation and confirm nothing survives

This is the headline check. We need a real long-running child process.

Create a scratch script inside the repo (the resolver only runs scripts inside a
content root, which is itself the subject of step 7):

```bash
$ mkdir -p scenarios/r01-scratch
$ cat > scenarios/r01-scratch/long.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "##snowops:step:phase-one"
echo "starting work"
# A grandchild, the way helm spawns its own children.
sleep 300 &
echo "spawned child $!"
wait
EOF
$ chmod +x scenarios/r01-scratch/long.sh
```

🔍 Before starting, note what is already running so you can tell new from old:

```bash
$ pgrep -fl 'sleep 300' ; echo "(nothing above = clean start)"
```

Now run it. The `runs` command reads the same database the engine writes, so
open a second terminal (remember to export `SNOWOPS_HOME` there too).

> **Note:** wiring `lab`/`platform` onto the engine is W3. Until then, drive the
> engine from this repository's own test harness:

```bash
$ go test -race -run 'TestCancel/cancels_a_running_run' ./internal/run/ -v
```

**Expect:** `PASS`, and the run reaching `cancelled` with `cancelled by user`
in its transcript.

Then confirm the real-process behaviour directly:

```bash
$ go test -race -count=1 -run 'TestExecCancellation/kills_the_whole' ./internal/toolchain/ -v
$ pgrep -fl 'sleep 60' ; echo "exit=$?"
```

**Expect:** no matching processes (`exit=1` from `pgrep` means "none found").
**A surviving process here is a blocking finding** — it is precisely the v1 bug
this wave exists to fix.

---

## 4. Killing labctl mid-run leaves a coherent record

```bash
$ go test -race -count=1 -run 'TestRecoveryOnStart' ./internal/run/ -v
```

**Expect:** `PASS`. The test simulates a process that died with a run in
flight, reopens the database, and asserts four things — verify the assertions
read the way you would expect by skimming
`internal/run/engine_test.go:TestRecoveryOnStart`:

- the run is `cancelled`, not still `running`,
- its error explains the interruption,
- **its partial log survived**,
- **its lock was released** (otherwise that lab is wedged forever).

🔍 Reason about the failure mode: if recovery did not run, what would happen the
next time you tried to operate on that lab? (Answer: a permanent lock conflict
with a run that no longer exists.) Note whether the released-lock assertion is
present.

---

## 5. Conflicting operations are refused, not raced

```bash
$ go test -race -count=1 -run 'TestLockConflict' ./internal/run/ -v
```

**Expect:** all subtests `PASS`. The first one asserts the refusal arrives in
under 100ms and that its message names the holding run and how to cancel it.

🔍 Read the message format in `internal/run/engine.go` (`LockConflictError.Error`).
**Judge it as a user:** if you hit this at 3am, does it tell you what is
happening and what to do? Note any wording you would change.

---

## 6. No output is lost, including across a disconnect

```bash
$ go test -race -count=1 -run 'TestReadLogs_CursorResume' ./internal/store/ -v
```

**Expect:** `PASS`. It writes 100 lines, reads them back in pages of 7 —
"disconnecting" between each — and fails on any gap or duplicate.

```bash
$ go test -race -count=1 -run 'TestSubscribe/a_slow_subscriber' ./internal/run/ -v
```

**Expect:** `PASS`. A subscriber that never drains its channel must not cost a
single persisted line. This is the v1 bug where a non-blocking channel send
dropped log lines silently.

---

## 7. Scripts cannot escape their content root

```bash
$ go test -race -count=1 -run 'TestResolverContainment' ./internal/toolchain/ -v
```

**Expect:** all subtests `PASS`, including the symlink case — a link that sits
inside the root but points outside it must be refused, since checking only the
cleaned path would let it through.

🔍 Confirm by hand that a traversal is refused:

```bash
$ ./bin/labctl runs logs ../../../etc/passwd
```

**Expect:** a clear error, not a file dump.

---

## 8. `labctl runs` is usable

```bash
$ ./bin/labctl runs list --help
$ ./bin/labctl runs logs --help
$ ./bin/labctl runs cancel --help
```

**Expect:** each explains itself without needing the source. Note anything
unclear — the CLI reference is generated from these.

```bash
$ ./bin/labctl runs cancel run_does_not_exist
```

**Expect:** an error naming the ID and suggesting `labctl runs list`.

---

## 9. `labctl doctor` diagnoses a broken environment

Covered fully by R02, but confirm one thing here: doctor must work even when
the environment is too broken for anything else to start.

```bash
$ PATH=/nonexistent ./bin/labctl doctor ; echo "exit=$?"
```

**Expect:** the table renders, every tool is reported missing, and the exit is
non-zero. **A crash or a config-loading error is a finding** — a diagnostic
that needs a healthy environment is useless.

---

## 10. Teardown

```bash
$ rm -rf scenarios/r01-scratch
$ rm -rf "$SNOWOPS_HOME"
$ unset SNOWOPS_HOME
$ pgrep -fl 'sleep 300' ; echo "(nothing above = clean)"
$ git status --short          # should show no leftover scratch files
```

---

## Results

| # | Step | Pass / Fail | Notes |
|---|---|---|---|
| 1 | Database created at `SNOWOPS_HOME` | | |
| 2 | Cancellation suite passes on this OS | | |
| 3 | **No process survives cancellation** | | |
| 4 | Interrupted run recovered, log intact, lock released | | |
| 5 | Lock conflict refused immediately and clearly | | |
| 6 | No log lines lost across disconnect or slow reader | | |
| 7 | Script traversal refused | | |
| 8 | `labctl runs` is self-explanatory | | |
| 9 | `doctor` works in a broken environment | | |

**Environment:** OS + version ______ · Go ______ · Arch ______

Steps 3, 4 and 6 are **blocking** — they are Wave 1's reason for existing.
Report failures as issues labelled `runbook-finding`, titled `R01 step N: …`.
