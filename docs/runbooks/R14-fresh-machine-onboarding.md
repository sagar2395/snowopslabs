# R14: Fresh-machine onboarding

**Wave:** W8 · **Time:** ~45 minutes per OS · **Cluster needed:** created by the runbook

The `onboarding` CI job proves the install-and-init path on a fresh Linux
runner. GitHub has no runner that can start colima or run WSL with Docker, so
macOS and WSL2 are proved here, by hand, on the new-user path from the README.

**Read this runbook as a new user.** Follow only what the README and the
getting-started page for your OS say. Every time you need knowledge that is not
on the page, that is a finding.

---

## Preconditions

- A machine you can test on without disturbing your own lab. On macOS, the
  isolated setup below keeps your real `~/.colima`, `~/.kube` and `~/.snowops`
  untouched.
- 8 GB of free RAM and about 15 GB of free disk.
- A release to install, or a snapshot built with
  `cd src && goreleaser release --snapshot --clean --skip=publish`.

### macOS: an isolated new user

colima's socket path must stay under 104 characters, so keep the fake home
short:

```bash
$ export HOME=/tmp/newuser && mkdir -p "$HOME"
$ export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
```

To test the installer against a local snapshot instead of GitHub:

```bash
$ mkdir -p /tmp/newuser/rel/download/v<version>
$ cp src/dist/labctl_<version>_darwin_arm64.tar.gz src/dist/checksums.txt /tmp/newuser/rel/download/v<version>/
$ export SNOWOPS_RELEASE_URL=file:///tmp/newuser/rel SNOWOPS_VERSION=<version>
```

and clone the branch under test instead of `stable` in step 1
(`--branch <branch>`); `SNOWOPS_VERSION` lets `install.sh` accept its `-dev`
`LAB_VERSION`.

### WSL2: a fresh distro

In PowerShell: `wsl --install -d Ubuntu-24.04 --name snowops-test`, then open
it. Run the runbook twice: once with Docker Desktop's WSL Integration off (so
`init` installs Docker Engine), once with it on.

---

## 1. Clone and install

In a directory of your choice (on WSL: `cd ~` first):

```bash
$ git clone --branch stable https://github.com/snowops-oss/snowopslabs.git
$ cd snowopslabs && ./install.sh
$ cd ~ && labctl scenario list | head -3     # works outside the clone
```

**Expect:** `Installed labctl <version> to ~/.local/bin/labctl`, `Your lab is
<path to the clone>`, a PATH hint if `~/.local/bin` is not on your `PATH`, and
the next steps. No password prompt. `~/.snowops/lab-dir` holds the clone's
path, and `scenario list` works from `~`.

The PATH hint is one command you can paste as it is, for the shell you are
logged in with: `~/.zshrc` on zsh, `~/.bash_profile` on macOS bash,
`~/.bashrc` on Linux bash. The command appends the `export PATH=` line to that
file, creating it when it is missing, and sources it, so `labctl` works in the
current terminal too. On a shell the installer does not recognise (fish, for
example) it prints the `export PATH=` line to add by hand instead.

## 2. Doctor on an empty machine

```bash
$ labctl doctor ; echo "exit=$?"
```

**Expect:** kubectl/helm/k3d marked missing, and a `Docker:` line.

- macOS with colima installed but never started: a note that `labctl init`
  starts colima at 2 CPU / 4 GB.
- Linux/WSL with no Docker: Docker `MISSING`.

🔍 Does each problem say what to run?

## 3. Build the lab ⚠️

```bash
$ time labctl init
```

**Expect:**

- Tools installed without sudo (Homebrew on macOS, `~/.local/bin` on Linux).
  On Linux/WSL the Docker Engine install asks for sudo, then stops and asks you
  to log out and back in (WSL: `wsl --shutdown`); run `labctl init` again.
- colima started at 2 CPU / 4 GB (macOS).
- `Docker has … (minimum 2 CPU / 4 GB)`.
- Cluster images downloaded with visible progress.
- `API server ready; all nodes Ready.` then `=== Lab is up ===` with Grafana
  and Prometheus URLs.
- Under 15 minutes on a first run.

**Failure signatures:** `TLS handshake timeout` (Docker too small - `init` should
have refused first; that is a finding); a silent pause longer than 10 minutes (a
stalled download that was not reported).

## 4. First scenario

```bash
$ labctl ui &                                      # http://localhost:3939
$ labctl scenario up observability-sre --deploy-prereqs
$ labctl scenario verify observability-sre
$ labctl status
```

**Expect:** the dashboard shows **Cluster reachable** and the installed
components; the scenario activates; verify shows PASS for the platform checks
and PENDING for the learner's work; `grafana-reachable` passes.

## 5. Capacity

```bash
$ labctl scenario up event-driven-arch --deploy-prereqs
```

**Expect** on a 4 GB Docker: blocked, before anything is installed, with
`labctl scenario down observability-sre` and/or the resize command for your
setup. Follow one of them; it then starts.

## 6. Reboot

Stop Docker the way a reboot would (`colima stop`; WSL: `wsl --shutdown`), then:

```bash
$ labctl status
$ labctl init
```

**Expect:** `status` and the dashboard say **unreachable** and point at
`labctl init`; `init` brings the lab back within about three minutes with the
same apps and active scenarios (`kubectl get ns go-api` keeps its creation
time).

## 7. Upgrade and uninstall ⚠️

On a branch of your own, edit a values file and commit it, then upgrade as the
README says (`git fetch origin && git merge origin/stable && ./install.sh`).
**Expect:** your commit is kept, the same URLs work, `.env` edits are kept,
active scenarios are still listed (state is in `~/.snowops/state/<cluster>/`,
not the clone), and no command prints a version warning.

Then follow the README's *Uninstall* section. **Expect:** no `labctl`, no
`~/.snowops`, no lab containers (`docker ps -a`), and on macOS no colima VM.

---

## Sign-off

| Step | macOS | WSL (Docker Desktop) | WSL (Docker Engine) |
|---|---|---|---|
| 1. Clone + install without sudo; works outside the clone | | | |
| 2. Doctor explains the empty machine | | | |
| 3. init end to end (time: ___) | | | |
| 4. First scenario + dashboard | | | |
| 5. Capacity block and advice | | | |
| 6. Reboot recovery keeps the lab | | | |
| 7. Upgrade keeps edits and state; uninstall is clean | | | |

Anything you had to look up that the README did not tell you: _____
