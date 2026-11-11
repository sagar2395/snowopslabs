# Getting started on Windows (WSL2)

The lab runs inside WSL2, as it would on Linux. Run every command in your WSL
distro's terminal (Ubuntu is recommended), never in PowerShell.

## What you need

- **WSL2 with a Linux distro.** In PowerShell: `wsl --install -d Ubuntu`.
- **Docker, one of:**
  - **Docker Desktop** with Settings → Resources → **WSL Integration** turned
    on for your distro, or
  - **Docker Engine inside the distro**, which `labctl init` installs if
    neither is present.
- **2 CPUs and 4 GB of memory for WSL** ([Resources](../resources.md)).

## Keep the lab on the Linux filesystem

Clone in your Linux home directory (`cd ~`), not under `/mnt/c`, and with the
distro's `git`, not Git for Windows. Windows drives are much slower from WSL,
and a checkout made from Windows can get Windows line endings, which break
every shell script. `labctl doctor` warns when your lab is on a Windows drive.

## Install and build the lab

In your distro's terminal:

```bash
cd ~
git clone --branch stable https://github.com/snowops-oss/snowopslabs.git
cd snowopslabs
./install.sh
labctl init
```

What `init` tells you, and what to do:

| Message | Do this |
|---|---|
| Docker Desktop is not enabled for this WSL distro | Docker Desktop → Settings → Resources → WSL Integration → enable your distro, Apply & Restart, reopen the terminal. |
| Docker Desktop (or Rancher Desktop) is installed on Windows but not running | Start it on Windows, then reopen the terminal and run `labctl init` again. labctl will not install a second Docker engine next to it unless you ask: to use Docker Engine inside the distro instead, keep the Windows app stopped (or uninstall it) and run `SNOWOPS_NATIVE_DOCKER=1 labctl init`. |
| added to the docker group | Run `wsl --shutdown` in PowerShell, reopen the terminal, run `labctl init` again. |
| WSL is running without systemd | Docker will not start by itself. Add `[boot]` and `systemd=true` to `/etc/wsl.conf`, then `wsl --shutdown`; or run `sudo service docker start` in each session. |

## Memory

Docker's memory comes from WSL, whether you use Docker Desktop or Docker
Engine. Set it in `%UserProfile%\.wslconfig` on Windows:

```ini
[wsl2]
memory=6GB
processors=4
```

then run `wsl --shutdown` in PowerShell and reopen the terminal.

## Opening the lab from Windows

- **The dashboard** (`labctl ui`, `http://localhost:3939`) works in your Windows
  browser as is, and `labctl ui` opens it for you (through `powershell.exe`, or
  `wslview` if you have `wslu`).
- **Lab URLs** such as `http://grafana.snowops.localhost` open in your Windows
  browser as they are. The browser resolves `*.localhost` to Windows' own
  localhost, and WSL2 forwards Windows' localhost ports into the distro (Docker
  Desktop publishes them on Windows directly). Nothing goes in a hosts file.
- A lab built before URLs moved to `*.localhost` has `*.k3d.local` names,
  which a Windows browser looks up in the Windows hosts file
  (`C:\Windows\System32\drivers\etc\hosts`), not WSL's. Run `labctl init`:
  it moves the lab to `*.localhost` names in place and keeps your apps and
  scenarios.

## After a restart

`wsl --shutdown` or a Windows restart stops the cluster. So does closing every
WSL terminal: once no terminal is open, WSL stops the distro after a short idle
time, and Docker and the cluster stop with it. Keep one WSL terminal open while
you use the lab (the one running `labctl ui` is enough).

When you come back, run `labctl init`; it restarts the cluster in order and
keeps your apps and scenarios.

## Troubleshooting

Common issues when running `labctl` on Windows with WSL2, organized by symptom.
`labctl doctor` checks your machine and prints these recommendations when it
detects a WSL environment.

### The browser does not open (`labctl ui`)

- **Symptom:** Running `labctl ui` prints `dashboard at http://localhost:3939` but does not automatically launch your browser on Windows.
- **Cause:** WSL interop is turned off in your WSL configuration, or `wslview` (from the `wslu` package) is not installed. `labctl` attempts to open the URL using `wslview`, then `powershell.exe`, `cmd.exe`, and finally `xdg-open`.
- **Fix:**
  - Open `http://localhost:3939` directly in your Windows browser.
  - To enable automatic browser opening, install `wslu`:
    ```bash
    sudo apt update && sudo apt install -y wslu
    ```
  - Verify WSL interop is enabled in `/etc/wsl.conf`:
    ```ini
    [interop]
    enabled = true
    appendWindowsPath = true
    ```
    Then restart WSL from Windows PowerShell: `wsl --shutdown`.

### Ingress hostnames do not resolve (`*.snowops.localhost` or custom domains)

- **Symptom:** `http://localhost:3939` opens the dashboard, but navigating to service URLs (such as `http://grafana.snowops.localhost`) fails with DNS resolution errors (`DNS_PROBE_FINISHED_NXDOMAIN` or `Server Not Found`) in your Windows browser.
- **Cause:** Modern Windows browsers resolve `*.localhost` natively to `127.0.0.1`, which WSL2 port forwarding routes into the distro. However, if your environment blocks `.localhost`, or if your cluster uses a custom `DOMAIN_SUFFIX` (or an older `.k3d.local` domain), the Windows browser queries the Windows DNS resolver and Windows hosts file (`C:\Windows\System32\drivers\etc\hosts`). Entries added by `labctl hosts add` inside WSL only update `/etc/hosts` in the Linux distro, which Windows browsers cannot see.
- **Fix (Recommended - Wildcard DNS):** Set `DOMAIN_SUFFIX` to use a public wildcard DNS resolver such as `nip.io` in `.env`:
  ```bash
  echo "DOMAIN_SUFFIX=127.0.0.1.nip.io" >> .env
  labctl init
  ```
  `*.127.0.0.1.nip.io` resolves to `127.0.0.1` automatically on Windows without editing any hosts file.
- **Fix (Migrate to `.localhost`):** If your cluster was built with an older release using `.k3d.local`, run `labctl init`. It updates ingress rules to `*.localhost` in place while preserving your deployed apps and scenarios.
- **Fix (Manual Windows hosts file edit):** If you must keep a custom non-localhost suffix without public DNS, add the hostnames manually to Windows' hosts file. Open PowerShell as Administrator:
  ```powershell
  Add-Content -Path "C:\Windows\System32\drivers\etc\hosts" -Value "127.0.0.1 grafana.snowops.localhost"
  ```
  Alternatively, access the services directly from inside WSL via `curl http://grafana.snowops.localhost`, where WSL's `/etc/hosts` applies.

### WSL keeps overwriting `/etc/hosts` on restart

- **Symptom:** Entries added by `labctl hosts add` in `/etc/hosts` disappear whenever WSL restarts or when the computer reboots.
- **Cause:** By default, WSL regenerates `/etc/hosts` from Windows hosts configuration on every distribution startup.
- **Fix:** Prevent WSL from generating `/etc/hosts` by adding the following under `[network]` in `/etc/wsl.conf`:
  ```ini
  [network]
  generateHosts = false
  ```
  Then restart WSL from PowerShell:
  ```powershell
  wsl --shutdown
  ```

### Docker Desktop WSL integration is off or daemon is not running

- **Symptom:** `labctl init` or `labctl doctor` prints:
  `Docker Desktop is not enabled for this WSL distro` or `Docker Desktop (or Rancher Desktop) is installed on Windows but not running`.
- **Cause:** Docker Desktop runs on the Windows host, but integration is not toggled on for your specific WSL distribution, or the Docker Desktop app is stopped.
- **Fix:**
  - Start Docker Desktop on Windows.
  - In Docker Desktop, go to **Settings** (gear icon) → **Resources** → **WSL Integration**.
  - Toggle **ON** your installed distribution (e.g. `Ubuntu`).
  - Click **Apply & Restart**, then reopen your WSL terminal and rerun `labctl init`.
  - *To use native Docker inside WSL instead:* If you do not want to use Docker Desktop, stop or uninstall Docker Desktop on Windows and run:
    ```bash
    SNOWOPS_NATIVE_DOCKER=1 labctl init
    ```

### Scripts fail with `$'\r': command not found` or poor disk performance

- **Symptom:** Running `./install.sh` or `./bin/labctl` fails with `$'\r': command not found`, `syntax error near unexpected token`, or file operations are unusually slow.
- **Cause:** The repository was cloned on a Windows drive (e.g. `/mnt/c/...`) or using Git for Windows, which converts line endings from LF to CRLF. Windows drives accessed through WSL also incur heavy 9P filesystem bridge overhead.
- **Fix:** Clone the lab inside the native Linux filesystem within your WSL home directory (`cd ~`) using the distro's `git`:
  ```bash
  cd ~
  git clone --branch stable https://github.com/snowops-oss/snowopslabs.git
  cd snowopslabs
  ./install.sh
  ```

### Docker does not start after `wsl --shutdown`

- **Symptom:** After restarting WSL or your machine, running `docker ps` returns `Cannot connect to the Docker daemon`.
- **Cause:** Your WSL distribution is running without `systemd`, so the Docker background service does not start automatically on boot.
- **Fix:** Enable `systemd` in `/etc/wsl.conf`:
  ```ini
  [boot]
  systemd = true
  ```
  Restart WSL (`wsl --shutdown` in PowerShell). Alternatively, start the service manually in each terminal session:
  ```bash
  sudo service docker start
  ```

### Cluster stops or is unreachable after laptop sleep or idle

- **Symptom:** The cluster is unresponsive or `labctl status` reports unreachable after waking up the laptop or after closing all terminal windows.
- **Cause:** When all WSL terminal windows are closed, WSL terminates the background VM after an idle timeout, stopping Docker and the cluster containers.
- **Fix:**
  - Keep at least one WSL terminal open while working (e.g. the terminal running `labctl ui`).
  - When returning, run `labctl init` to restore the cluster in order while preserving your apps and scenarios.

## Uninstall

```bash
labctl teardown
rm -rf ~/.snowops ~/.local/bin/labctl   # and your clone, when you no longer need it
```

