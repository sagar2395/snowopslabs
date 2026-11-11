# Getting started on macOS

Works on Apple Silicon and Intel Macs.

## What you need

- **Homebrew.** If you don't have it, `labctl init` installs it; that is the one
  step that asks for your password.
- **2 CPUs and 4 GB of memory for Docker**, more to run several scenarios at
  once ([Resources](../resources.md)).
- **About 10 GB of free disk.**

You don't need Docker Desktop. `labctl init` installs
[colima](https://github.com/abiosoft/colima), a lightweight Docker VM, with
Homebrew and starts it at 2 CPU / 4 GB. If you already use Docker Desktop, the
lab uses it instead; give it at least 2 CPUs and 4 GB under Settings →
Resources.

## Install and build the lab

Clone the latest release wherever you keep projects, then install the matching
`labctl` and build the lab:

```bash
git clone --branch stable https://github.com/snowops-oss/snowopslabs.git
cd snowopslabs
./install.sh
labctl init
```

`git` comes with Apple's Command Line Tools; if they are missing, the first
`git` command offers to install them. `install.sh` puts `labctl` in
`~/.local/bin` and records your clone, so `labctl` works from any directory.
If `~/.local/bin` is not on your `PATH`, the installer prints one command that
adds it to `~/.zshrc` (or `~/.bash_profile` on bash), creating that file for
you if it is missing - paste it as it is, then open a new terminal.

`init` installs `kubectl`, `helm`, `k3d`, `colima`, `docker` and
`docker-buildx` with Homebrew (a tool you already have at a new enough version
is left alone), starts colima, creates the cluster and installs the platform.
It finishes with the lab's URLs. Then:

```bash
labctl ui                  # http://localhost:3939
```

## Giving colima more memory

Set the size in `.env` in your clone; `labctl init` starts a *stopped* colima
at that size, and never shrinks it:

```bash
LAB_CPUS=4
LAB_MEMORY=8
```

A colima that is already running is never resized for you, because restarting
it stops your other containers. Resize it yourself, then bring the lab back:

```bash
colima stop && colima start --cpu 4 --memory 8
labctl init
```

## After a reboot

colima does not start on its own. Run `labctl init`: it starts colima at the
lab's size, restarts the cluster and waits until everything is Ready, keeping
your apps and scenarios. It also checks that Grafana can query Prometheus and
repairs Grafana if not, so the dashboards have data when it says the lab is up.

## Uninstall

```bash
labctl teardown
labctl hosts remove                  # only if you added hosts entries for an older lab
rm -rf ~/.snowops ~/.local/bin/labctl   # and your clone, when you no longer need it
colima delete --data                 # removes the VM and every image in it
```

`colima delete` without `--data` keeps Docker's images and containers for the
next VM.
