# R02 — Doctor & Preflight

**Wave:** W1 · **Time:** ~15 minutes · **Cluster needed:** no

`labctl doctor` exists so a broken environment produces one clear sentence
instead of an opaque failure forty seconds into a cluster build. That only works
if the messages are actually good, which is a judgement a person has to make.

**Read this runbook as a reviewer, not just a tester.** For every message you
see, ask: *if I hit this at 3am, would it tell me what to do?* Note anything you
would reword — that feedback is the point of the exercise.

---

## Preconditions

- `bin/labctl` built (`make cli-build`).
- A scratch PATH directory so nothing on your real system is touched:

```bash
$ export R02_DIR=/tmp/snowops-r02
$ mkdir -p "$R02_DIR/bin"
```

Nothing here installs, upgrades or removes anything from your machine. Every
manipulation is a fake binary in a temporary directory placed ahead of your real
`PATH`.

---

## 1. A healthy environment reports clean

```bash
$ ./bin/labctl doctor ; echo "exit=$?"
```

**Expect:** a table with one row per tool, then either

```
✓ Everything SnowOps Labs needs is installed and current.
```

with `exit=0`, or a problems section listing what is genuinely missing on your
machine with a non-zero exit.

🔍 Whatever the outcome, check the table itself:

- Is every column meaningful at a glance (`TOOL`, `STATUS`, `VERSION`, `REQUIRED`)?
- Are optional tools (`k3d`, `kind`) visibly distinct from required ones?
- Does the version it detected match what `helm version` etc. actually report?

---

## 2. A missing required tool

Shadow `helm` with an empty directory entry — we do this by pointing `PATH` at a
directory that contains everything *except* helm.

```bash
$ ln -sf "$(command -v kubectl)" "$R02_DIR/bin/kubectl"
$ ln -sf "$(command -v bash)"    "$R02_DIR/bin/bash"
$ ln -sf "$(command -v docker)"  "$R02_DIR/bin/docker" 2>/dev/null || true
$ PATH="$R02_DIR/bin" ./bin/labctl doctor ; echo "exit=$?"
```

**Expect:** a non-zero exit, `helm` marked `MISSING`, and a problems line that:

- names the tool,
- says **what breaks without it** ("installing platform components and scenario charts"),
- gives a **platform-specific install command** (`brew install helm` on macOS, a
  URL on Linux).

🔍 **Judge it:** does that line contain everything you would need, without
opening documentation? Note anything missing.

---

## 3. An outdated tool

Put a fake `helm` that reports an old version ahead of the real one:

```bash
$ cat > "$R02_DIR/bin/helm" <<'EOF'
#!/usr/bin/env bash
echo "v3.9.0"
EOF
$ chmod +x "$R02_DIR/bin/helm"
$ PATH="$R02_DIR/bin:$PATH" ./bin/labctl doctor ; echo "exit=$?"
```

**Expect:** non-zero exit, `helm` marked `OUTDATED`, and a message showing
**both** the version found (`3.9.0`) and the version required (`3.12.0`), plus
the upgrade hint.

🔍 A message that says only "helm is too old" would be a finding. Confirm both
numbers are present.

---

## 4. A tool whose version cannot be parsed

Version output formats change. SnowOps Labs must not block your work because it
failed to parse one.

```bash
$ cat > "$R02_DIR/bin/helm" <<'EOF'
#!/usr/bin/env bash
echo "helm, the package manager for Kubernetes"
EOF
$ chmod +x "$R02_DIR/bin/helm"
$ PATH="$R02_DIR/bin:$PATH" ./bin/labctl doctor ; echo "exit=$?"
```

**Expect:** `helm` shown as `unknown`, a note saying we are continuing anyway,
and **`exit=0`** if nothing else is broken.

🔍 This is a deliberate design choice: our inability to parse a version is our
problem, not yours. Confirm it does not block, and that the note is
non-alarming.

---

## 5. Optional tools warn but do not block

```bash
$ rm -f "$R02_DIR/bin/helm"
$ PATH="$R02_DIR/bin:$PATH" ./bin/labctl doctor 2>&1 | grep -A3 'Notes:'
```

**Expect:** if `k3d` or `kind` is absent on your machine, each appears under
`Notes:` rather than `Problems to fix`, and each hint mentions the alternative
("or use PROFILE=kind").

🔍 Confirm the exit code is driven only by *required* tools.

---

## 6. Docker not running

Docker being installed but not running is the single most common real-world
failure for a laptop-first tool.

**macOS (Colima):**
```bash
$ colima stop
$ ./bin/labctl doctor
$ colima start          # restore when done
```

**Linux:**
```bash
$ sudo systemctl stop docker
$ ./bin/labctl doctor
$ sudo systemctl start docker   # restore when done
```

**Expect:** doctor still completes and renders its table — it must not hang or
crash. Docker's client version is usually still reported even with the daemon
down.

🔍 **This is a known gap worth your judgement.** Preflight currently checks that
binaries exist and are current; it does not yet probe daemon or cluster
reachability. Does the output leave you misled into thinking everything is fine?
If so, say so — that becomes a W3 task (`lab status --live` is the planned
home for reachability checks).

---

## 7. Doctor works when nothing else can

```bash
$ PATH=/nonexistent ./bin/labctl doctor ; echo "exit=$?"
```

**Expect:** the table renders with everything missing, and a non-zero exit.
**A crash, a panic, or a config-loading error is a blocking finding** — the
command that diagnoses a broken environment must not need a working one.

---

## 8. The messages match the automated expectations

```bash
$ go test -run 'TestRunDoctor|TestRequirements' ./internal/cli/ ./internal/toolchain/ -v
```

**Expect:** all `PASS`. These assert on message *content* — that a missing tool
names the reason and the fix, that the bash minimum still accommodates the 3.2
macOS ships, and that no cloud CLI is required after ADR-0001.

---

## 9. Teardown

```bash
$ rm -rf "$R02_DIR"
$ unset R02_DIR
$ command -v helm kubectl docker    # your real tools, unchanged
```

Confirm Docker/Colima is running again if you stopped it in step 6.

---

## Results

| # | Step | Pass / Fail | Message quality (1–5) | Notes |
|---|---|---|---|---|
| 1 | Healthy environment reports clean | | | |
| 2 | Missing tool: names tool, reason, fix | | | |
| 3 | Outdated tool: shows both versions | | | |
| 4 | Unparseable version does not block | | | |
| 5 | Optional tools warn only | | | |
| 6 | Docker stopped: no hang or crash | | | |
| 7 | Works with a broken PATH | | | |
| 8 | Automated message assertions pass | | | |

**Environment:** OS + version ______ · Arch ______

Step 7 is **blocking**. Steps 2–4 are judged on message quality as much as on
pass/fail — a low score there is worth reporting even when the step technically
passes.
