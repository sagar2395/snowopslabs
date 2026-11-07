# Troubleshooting

labctl's errors say what went wrong and print the command that fixes it. This
page collects them by symptom. `labctl doctor` checks your machine, and
`labctl status` checks the lab.

## Installing and building the lab

| Symptom | Cause and fix |
|---|---|
| `labctl: command not found` after installing | `~/.local/bin` is not on your `PATH`. The installer prints one command that adds it to the right startup file (`~/.zshrc`, `~/.bash_profile` or `~/.bashrc`); paste it as it is, or add `export PATH="$HOME/.local/bin:$PATH"` to that file yourself and open a new terminal. |
| `could not find your lab` | labctl does not know where your clone is. Run `./install.sh` in your clone once (it records it; `make cli-install` does the same for a source build), `cd` into the clone, or set `SNOWOPS_LAB_DIR`. |
| `the lab recorded in ~/.snowops/lab-dir … is gone` | You moved or deleted your clone. Run `./install.sh` in its new place. |
| `labctl is X but the lab in … is Y` | Your clone moved to another release (for example after `git merge origin/stable`). Run `./install.sh` in the clone to get the matching labctl. |
| `… is development content` | You cloned `main`. Clone `--branch stable`, or `git switch stable`, then `./install.sh`. |
| `install.sh`: `this checkout is development content` | Same: switch to `stable`, or build labctl from source (CONTRIBUTING). |
| `lab state is in both … and …` | An older labctl kept state inside the clone (`.labctl/`). labctl now uses `~/.snowops/state/<cluster>/`; copy what you need from `.labctl/`, then delete it. |
| `docker is too small for the lab` | Docker has less than 2 CPU / 4 GB. Resize it with the printed command (colima, Docker Desktop, `.wslconfig` or native Linux), then `labctl init`. |
| `the Docker daemon is not running` | Start it with the printed command. On macOS with colima, `labctl init` starts it for you. |
| `the Docker daemon refused this user` | You were just added to the `docker` group. Log out and back in (WSL: `wsl --shutdown` in PowerShell), then `labctl init`. |
| `this WSL distro has Docker Desktop integration turned off` | Docker Desktop → Settings → Resources → WSL Integration → enable your distro. |
| `could not download <image>` | A registry download stalled twice. Check your connection, proxy or VPN, then `labctl init` again; it resumes. |
| `the platform did not install cleanly` | Usually Docker is short of memory (`TLS handshake timeout`, pods Pending). `labctl doctor`, then `labctl init`. Installs are safe to repeat. |
| `setup-tools`: a tool `is older than` its minimum after installing | An older copy earlier on your `PATH` shadows the new one. Remove it, or put `~/.local/bin` (Homebrew's directory on macOS) earlier on `PATH`. |

## Using the lab

| Symptom | Cause and fix |
|---|---|
| The dashboard or `labctl status` says the cluster is **unreachable** | Docker or colima is stopped, usually after a reboot, or out of memory. Run `labctl init`; it keeps your apps and scenarios. |
| `Not enough memory for <scenario>` | Run the commands it prints: `labctl scenario down` for a scenario, `labctl platform down` for components only it used or nothing uses. Or resize Docker as printed. [Resources](resources.md) lists what each scenario needs. |
| `platform prerequisite(s) not installed` or `required app(s) not deployed` | Install what it names, or re-run with `--deploy-prereqs`. |
| An app pod is in `ImagePullBackOff` | Its image was not built into the cluster. `labctl app build <name>`, or use `--deploy-prereqs`. |
| URLs end in `:8080` | Something else holds port 80, so the ingress moved to 8080. Every URL and check follows it. To get 80 back, free it and `labctl reset`. |
| Grafana dashboards show No data: a warning on the App picker, an empty Namespace picker, or `Unable to find datasource plugin` / `Plugin not registered` | Grafana cannot query Prometheus. Run `labctl init`: it checks that Grafana can query Prometheus and re-applies Grafana when it cannot (`labctl platform up monitoring/grafana` does the same by hand). If init still reports `grafana still cannot query Prometheus`, check the Prometheus pod runs: `kubectl -n monitoring get pods`. If you changed Grafana's admin password, set `GRAFANA_ADMIN_PASSWORD` in `.env` so init can run the check. |
| A node stays `NotReady` after a restart | `labctl init` restarts the cluster and corrects the address Kubernetes records for any node whose container came back with a different IP (the server included), keeping its pods. If it still fails, `labctl reset` rebuilds the lab from scratch. When `labctl status` names a node whose k3s logged `too many open files`, run the commands it prints. |
| `<scenario> needs 2 agent nodes` on kind | kind cannot add nodes to a running cluster. Set `AGENTS=2` in `.env` and `labctl reset`. |
| `<scenario> must run on its own` | Bring down the other active scenarios first. |
| A lab URL shows another page, an empty reply, or nothing | Another lab or program on this machine (another user's Docker VM, for one) answers on the lab's port. `labctl init` checks this and moves a k3d lab to a free port; on kind, free the port or set `HTTP_PORT` / `HTTPS_PORT` and `labctl reset`. |
| A check says Prometheus or Grafana returned 404 | Another service on the lab's port answered instead of the lab. Re-run `labctl init`; it confirms the lab answers and moves it if not. |
| URLs end in `.k3d.local`, need `/etc/hosts` entries, or take 5 seconds to open on a Mac | The lab was built before URLs moved to `*.localhost` (macOS sends `.local` lookups to Bonjour, which is slow). Run `labctl init`: it moves the lab to `*.localhost` names in place, keeping your apps and scenarios, and those names need nothing. Then `labctl hosts remove` drops the old `/etc/hosts` entries. To keep the old names, set `DOMAIN_SUFFIX=k3d.local` in `.env`. |
| `labctl init` still prints `.k3d.local` URLs after you updated the clone | The `labctl` on your `PATH` is an older one, usually the release `install.sh` put in `~/.local/bin`, while `make cli-build` writes `./bin/labctl`. Run `./bin/labctl init`, or `make cli-install` to replace the installed one (`command -v labctl` shows which runs). A `labctl ui` started before the move shows the old URLs until you restart it. |
| `a kind cluster named 'snowops' already exists` (or k3d) | Lab names are unique per machine, because URLs and lab state are keyed by them. Set `CLUSTER_NAME` in `.env` to another name. |
| A pod you just started resolves names or connects where a NetworkPolicy should block it | On k3s the policy controller starts enforcing on a new pod a moment after it starts, so a test in its first second gets through. Wait a few seconds first: `kubectl run … -- sh -c 'sleep 5; nslookup <name>'`. |

## Windows (WSL2)

For a complete troubleshooting guide organized by symptom (covering browser
launching, DNS resolution (`*.localhost` vs `DOMAIN_SUFFIX=127.0.0.1.nip.io`),
`/etc/wsl.conf` (`generateHosts`, `systemd`, `interop`), Docker Desktop WSL
integration, line endings, and idle cluster recovery) see the
[WSL2 Troubleshooting guide](getting-started/wsl.md#troubleshooting).

## GitHub Codespaces

A setup that failed before installing labctl, Docker still starting during setup, and
`*.localhost` links that do not open in your browser are covered in
[Running the lab in GitHub Codespaces](getting-started/codespaces.md#when-something-goes-wrong).

## Recovering a stuck run

labctl records every operation as a run and holds a lock per lab resource while it works, so two operations cannot change the same thing at once. These messages appear when a run ended without releasing its lock, usually because labctl or the machine stopped mid-operation. `labctl runs list` shows the runs, and `labctl runs logs <run-id>` shows what each one did.

| Symptom | Cause and fix |
|---|---|
| `Recovery required` or a held key after labctl exited | Read `labctl runs logs <run-id>` and check the lab. Work or a child process may still be running. `labctl runs recover <run-id>` previews the override; add `--force` only when you intend to permit another operation on the same key. Ordinary cancellation does not release this key. |
| Recovery says an owner lock is busy | The owner may still be executing. Let it finish or stop it, then inspect the lab before retrying recovery. `--force` does not bypass a live owner. |
| `owner registration raced cleanup` | Retry the labctl command. Another engine repeatedly removed its unused registration file before it could take the lock. No work was accepted by that engine. |
| `could not clean unused owner files; inspect the owner directory` | Engine startup continues and other eligible files are still cleaned. Inspect the named unused entries in `<database>.owners` for invalid types or permissions. Do not change referenced or locked owner files. |
| Recovery reason is `owner_missing` | Startup continues, cancels that owner's queued work, and keeps running work's key and logs with recovery required. A missing file does not prove the work stopped. Check the lab, preview `labctl runs recover <run-id>`, then add `--force` only to permit new work on that key. The stronger warning and release audit retain the missing-owner uncertainty. Do not delete `<database>.owners` or recreate a referenced file. |
| Recovery cannot check an owner file for another reason | Keep the key reserved and repair the owner-file problem. `--force` does not bypass other probe errors. Do not create, replace or delete referenced files to bypass the check. Use the canonical database and owner directory on local storage. |
| Migration requires an offline store | Stop every labctl server, CLI and worker, back up the database, upgrade all entry points, then retry. Old binaries without upgrade locks must also be stopped; the new lock cannot detect them. |
| `Key released by operator` | The recorded actor explicitly released the key. The operation may still be running. Read the release audit and check the lab before starting replacement work. |
| `Could not remove traffic job`, `configmap` or `namespace` after cancelling start | The cleanup request failed. When the cluster responds, inspect the saved run with `labctl runs logs <id>` and run `labctl traffic stop`. Cleanup preserves a namespace or ConfigMap that existed before start. |

## Still stuck

`labctl runs list` shows every operation labctl ran and `labctl runs logs <id>`
its full output. Open an [issue](https://github.com/sagar2395/snowopslabs/issues)
with the output of `labctl doctor` and `labctl status`.
