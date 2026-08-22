# R00 — Environment & Build

**Wave:** W0 · **Time:** ~20 minutes · **Cluster needed:** no

Proves that a person who has never seen this repository can clone it, build it,
and run every quality gate — on their own machine, not just in CI. If this
runbook fails, no other wave's work can be trusted, because the thing that
verifies it does not run.

---

## Preconditions

| Requirement | Check | If missing |
|---|---|---|
| Go 1.24+ | `go version` | https://go.dev/dl/ |
| Node 22+ | `node --version` | https://nodejs.org or `brew install node` |
| bats | `bats --version` | `brew install bats-core` (macOS) / `npm install -g bats` |
| shellcheck | `shellcheck --version` | `brew install shellcheck` / `apt install shellcheck` |
| git | `git --version` | your package manager |

Optional but exercised below: `golangci-lint`, `gosec`, `govulncheck`, `shfmt`.

**Disk:** ~1.5 GB (Go module cache, node_modules, Playwright browser).
**Network:** required — this step downloads dependencies.

Run everything from a **fresh clone**. Reusing your working copy can pass on
stale artifacts and hide a missing file.

```bash
$ git clone <repo-url> /tmp/snowops-r00 && cd /tmp/snowops-r00
$ git checkout claude/platform-simulator-redesign-41mkry
```

---

## 1. 🔍 The repository is where the docs say it is

```bash
$ ls go.mod cmd/labctl/main.go internal/ pkg/ ui/ docs/ROADMAP.md
```

**Expect:** all present, no errors. `go.mod` is at the **root** — not under
`cmd/labctl/` (ADR-0005).

```bash
$ head -1 go.mod
```

**Expect:** `module github.com/sagar2395/snowopslabs`

---

## 2. The Go build is clean

```bash
$ go build ./...
$ go vet ./...
```

**Expect:** both silent, exit 0. First run takes a minute or two while modules
download.

**Failure signature — `no required module provides package`:** a leftover
import of something W0 deleted. Report the package name; it is a real bug.

---

## 3. Go tests pass with the race detector, and the coverage gate holds

```bash
$ make test-go
```

**Expect:** every package `ok`, then:

```
coverage gate passed (minimum 80%)
```

Takes 1–3 minutes. The race detector makes it slower than a plain `go test`;
that is intentional (ADR-0003, ADR-0004).

🔍 Now confirm the gate actually bites. Break a test deliberately:

```bash
$ printf '\nfunc TestDeliberateFailure(t *testing.T) { t.Fatal("R00 check") }\n' >> pkg/checks/runner_test.go
$ make test-go ; echo "exit=$?"
$ git checkout pkg/checks/runner_test.go
```

**Expect:** `FAIL` and a **non-zero** exit. A green result here means the gate
is not wired up — a blocking finding.

---

## 4. The coverage ratchet cannot be loosened

The exceptions file lets packages awaiting rewrite sit below 80%, but only
downward-never. Confirm:

```bash
$ head -20 .coverage-exceptions
```

**Expect:** a comment block explaining the rules, then entries of the form
`<package> <percent> <wave> <reason>`. Every entry names the wave that deletes
it.

```bash
$ bats test/shell/coverage_gate.bats
```

**Expect:** `1..12`, all `ok`. These assert the ratchet's behaviour directly:
an exception cannot be lowered, an exception that is no longer needed fails
until removed, and a stale entry fails.

---

## 5. Shell tests and the portability gate

```bash
$ make test-shell
```

**Expect:** `1..39`, all `ok` across three files (`harness.bats`,
`coverage_gate.bats`, `portability_gate.bats`).

```bash
$ bash scripts/lint-portability.sh
```

**Expect:** `portability gate passed`

🔍 Confirm this gate bites too. Golden rule 1 is the one most easily broken by
someone developing only on Linux:

```bash
$ echo 'readlink -f /tmp' >> platform/ingress/nginx/status.sh
$ bash scripts/lint-portability.sh ; echo "exit=$?"
$ git checkout platform/ingress/nginx/status.sh
```

**Expect:** a failure naming `readlink -f`, the file, and the line number, with
a non-zero exit.

---

## 6. Shell linting

```bash
$ make lint-shell
```

**Expect:** shellcheck silent, then the portability gate passing. If `shfmt`
is not installed you will see a skip notice — that is acceptable locally, but
CI installs it.

---

## 7. The UI builds, typechecks and tests

```bash
$ cd ui && npm ci
```

**Expect:** completes without `ERESOLVE` or peer-dependency errors.

```bash
$ npm run typecheck
```

**Expect:** silent. TypeScript runs in `strict` mode; any output is a failure.

```bash
$ npm run test:coverage
```

**Expect:** `Test Files 2 passed`, `Tests 17 passed`, followed by a coverage
table.

```bash
$ npm run build
```

**Expect:** a Vite build summary and `ui/dist/index.html` on disk.

---

## 8. UI journeys run in a real browser

```bash
$ npx playwright install chromium    # first time only
$ npm run test:e2e
```

**Expect:** `4 passed`. Playwright builds the SPA, serves it, and drives
Chromium.

> If your environment ships its own browser (a CI image or dev container), set
> `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/path/to/chromium` instead of downloading a
> second copy.

**Failure signature — `Executable doesn't exist`:** the browser is not
installed. Run the install command above, or set the variable.

---

## 9. The binary builds with the UI embedded

```bash
$ cd .. && make cli-build
```

**Expect:** ends with `Binary: bin/labctl`. This builds the SPA and copies it
into `internal/webui/dist/` before compiling, so the result is one
self-contained artifact.

```bash
$ ./bin/labctl --help
```

**Expect:** the command list. 🔍 **Confirm the removed commands are gone** —
these must each report an unknown command:

```bash
$ ./bin/labctl pack --help        ; echo "exit=$?"
$ ./bin/labctl credential --help  ; echo "exit=$?"
$ ./bin/labctl edition --help     ; echo "exit=$?"
```

**Expect:** `unknown command` and a non-zero exit for all three (ADR-0001).

---

## 10. Cross-compilation works for every release target

```bash
$ make cli-build-all
$ ls -la dist/
```

**Expect:** four binaries — `labctl-darwin-arm64`, `labctl-darwin-amd64`,
`labctl-linux-amd64`, `labctl-linux-arm64`.

This is the practical proof of the no-cgo rule (ADR-0002): cross-compilation is
a plain `GOOS`/`GOARCH` change. **A failure here means something pulled in cgo**
— report it, it blocks the wave.

---

## 11. 🔍 Cloud and marketplace surfaces are gone

```bash
$ ls runtimes/
```

**Expect:** exactly `k3d`, `kind`, `incluster`. No `aks`, `eks`, `gke`.

```bash
$ ls foundation 2>&1
$ ls registry packs 2>&1
```

**Expect:** "No such file or directory" for each.

```bash
$ grep -rn -iE 'marketplace|entitlement' --include='*.go' . | grep -v archive
```

**Expect:** no output.

---

## 12. Teardown

```bash
$ cd /tmp && rm -rf snowops-r00
```

Nothing in this runbook creates a cluster or touches your kubeconfig, so there
is nothing else to clean up.

---

## Results

| # | Step | Pass / Fail | Notes |
|---|---|---|---|
| 1 | Repository layout, module at root | | |
| 2 | `go build` and `go vet` clean | | |
| 3 | `make test-go` green; deliberate failure caught | | |
| 4 | Coverage ratchet behaves | | |
| 5 | Shell tests; portability gate catches a violation | | |
| 6 | `make lint-shell` clean | | |
| 7 | UI installs, typechecks, tests, builds | | |
| 8 | Playwright journeys pass | | |
| 9 | `make cli-build`; removed commands absent | | |
| 10 | Four cross-compiled binaries | | |
| 11 | Cloud and marketplace surfaces absent | | |

**Environment:** OS + version ______ · Go ______ · Node ______

Report failures as issues labelled `runbook-finding`, titled
`R00 step N: <what happened>`. A failing step blocks Wave 0.
