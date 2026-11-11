# Getting started on Linux

Works on amd64 and arm64 with any distro that runs Docker Engine. On Ubuntu,
Debian, Fedora, the RHEL family, Amazon Linux, Arch and Alpine, `labctl init`
installs Docker for you.

## What you need

- **Docker Engine.** `labctl init` installs it from Docker's repository for your
  distro if it is missing (this is the one step that uses sudo).
- **2 CPUs and 4 GB of free memory.** Docker runs on your machine directly, so
  the lab uses the host's memory; close memory-hungry programs if you are
  short ([Resources](../resources.md)).
- **About 10 GB of free disk.**
- **git** (`sudo apt install git` or your distro's equivalent).

## Install and build the lab

Clone the latest release wherever you keep projects, then install the matching
`labctl` and build the lab:

```bash
git clone --branch stable https://github.com/snowops-oss/snowopslabs.git
cd snowopslabs
./install.sh
labctl init
```

`install.sh` puts `labctl` in `~/.local/bin` and records your clone, so
`labctl` works from any directory.

`init` downloads `kubectl`, `helm` and `k3d` into `~/.local/bin` (no sudo; a
tool you already have at a new enough version is left alone) and prints the
line to add to your shell profile if that directory is not on your `PATH`.

### The docker group

If `init` installs Docker, it adds you to the `docker` group so you can use it
without sudo. Group membership only applies to new login sessions, so `init`
stops and asks you to log out and back in (or run `newgrp docker`), then run
`labctl init` again. The same message appears if Docker refuses you for this
reason later.

### Rootless Docker

Rootless Docker cannot bind ports below 1024, so the lab's ingress moves to
8080/8443. Every URL labctl prints, and every check, follows it.

### inotify limits

The lab raises `fs.inotify.max_user_instances` to 512 so log shippers can run.
On Linux the cluster's nodes share your kernel, so this changes the setting for
your whole machine until the next reboot.

## After a reboot

Docker usually starts on boot. Run `labctl init` to restart the cluster in
order and wait until it is healthy; your apps and scenarios are kept.

## Uninstall

```bash
labctl teardown
labctl hosts remove                  # only if you added hosts entries for an older lab
rm -rf ~/.snowops ~/.local/bin/labctl   # and your clone, when you no longer need it
```
