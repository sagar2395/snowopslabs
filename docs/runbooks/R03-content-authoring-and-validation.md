# R03 — Content authoring & validation

**Wave:** W2 · **Time:** ~15 minutes · **Cluster needed:** no

`labctl validate` is the gate that keeps every scenario, incident, learning path
and challenge honest: required fields present, cross-references resolvable,
templates well-formed. This runbook confirms it catches the mistakes an author
actually makes, and that the messages point at the exact file, line and
reference — not a vague "invalid".

**Read this as a reviewer.** For every failure message, ask: *would this tell me
what to fix without opening the loader source?*

---

## Preconditions

- Repo checked out; Go toolchain installed.
- Nothing here touches a cluster or your machine — validation is pure file I/O.

A scratch external content root for the later steps:

```bash
$ export R03_DIR=/tmp/snowops-r03
$ mkdir -p "$R03_DIR"
```

---

## 1. All in-repo content validates clean

```bash
$ go run ./cmd/labctl validate ; echo "exit=$?"
```

**Expect:** a single success line and `exit=0`, e.g.

```
✓ all content valid — 3 challenges, 6 incidents, 1 paths, 13 scenarios
```

If this fails on an untouched checkout, stop — the repo is broken, not your test.

---

## 2. A broken cross-reference names the file, line and reference

Point a scratch path at a scenario that does not exist, and load it as an
external root so the repo stays clean:

```bash
$ mkdir -p "$R03_DIR/learn/broken"
$ cat > "$R03_DIR/learn/broken/path.yaml" <<'YAML'
name: broken
displayName: Broken Path
modules:
  - name: step-one
    action:
      type: scenario
      ref: this-scenario-does-not-exist
    check:
      name: chk
      type: script
      script: x.sh
YAML
$ SNOWOPS_CONTENT_PATH="$R03_DIR" go run ./cmd/labctl validate ; echo "exit=$?"
```

**Expect:** `exit=1` and a line naming the file, the line of the bad `ref:`, and
the missing name, e.g.

```
/tmp/snowops-r03/learn/broken/path.yaml:7: [path/broken] module "step-one" references unknown scenario "this-scenario-does-not-exist"
```

**Reviewer check:** the line number should point at the `ref:` value.

---

## 3. An unknown template variable is an error, not a silent blank

```bash
$ mkdir -p "$R03_DIR/scenarios/tmpl"
$ cat > "$R03_DIR/scenarios/tmpl/scenario.yaml" <<'YAML'
name: tmpl
displayName: Template Typo
components:
  - name: c1
    type: script
    script: run.sh
checks:
  - name: reachable
    type: http
    url: "http://go-api.{{.DomainSufix}}/health"
YAML
$ SNOWOPS_CONTENT_PATH="$R03_DIR" go run ./cmd/labctl validate ; echo "exit=$?"
```

**Expect:** `exit=1` and a message about the misspelled key `DomainSufix`
(the correct key is `DomainSuffix`). A typo must fail here, never resolve to an
empty string that breaks a URL at run time.

---

## 4. An external content root is discovered and usable

Add a *valid* scenario to the scratch root and confirm it is counted:

```bash
$ rm -rf "$R03_DIR"/learn "$R03_DIR"/scenarios
$ mkdir -p "$R03_DIR/scenarios/my-extra"
$ cat > "$R03_DIR/scenarios/my-extra/scenario.yaml" <<'YAML'
name: my-extra
displayName: My Extra Scenario
components:
  - name: c1
    type: script
    script: run.sh
YAML
$ SNOWOPS_CONTENT_PATH="$R03_DIR" go run ./cmd/labctl validate
```

**Expect:** the scenario count rises by one versus step 1, and `exit=0`. The
external root was used without forking the repo (W2-T09).

---

## 5. Malformed YAML fails as a problem, not a crash

```bash
$ mkdir -p "$R03_DIR/scenarios/garbage"
$ printf 'name: garbage\n\t- : :\n' > "$R03_DIR/scenarios/garbage/scenario.yaml"
$ SNOWOPS_CONTENT_PATH="$R03_DIR" go run ./cmd/labctl validate ; echo "exit=$?"
```

**Expect:** `exit=1` with a YAML-parse problem for that file — never a panic or
stack trace. (The parsers are also fuzzed in CI: `make fuzz`.)

---

## Cleanup

```bash
$ rm -rf "$R03_DIR"
```

---

## Sign-off

| Step | Result | Notes |
|---|---|---|
| 1. In-repo content valid | | |
| 2. Cross-ref names file/line/ref | | |
| 3. Unknown template key errors | | |
| 4. External root discovered | | |
| 5. Malformed YAML → problem, not crash | | |

Message-quality judgement (would each failure guide a fix at 3am?): _____
