# CLI reference

`labctl` is the command-line interface for SnowOps Labs. It builds the lab,
runs scenarios, injects faults and grades your fix.

## Install

```bash
git clone --branch stable https://github.com/sagar2395/snowopslabs.git
cd snowopslabs && ./install.sh
```

`install.sh` puts the `labctl` release named by the clone's `LAB_VERSION` in
`~/.local/bin`, with no sudo, and records the clone in `~/.snowops/lab-dir`.
Re-run it after upgrading the clone (`git merge origin/stable`). The per-OS
guides are under [Getting started](../../getting-started/macos.md).

`labctl` finds the lab in this order: `--project-dir`; the clone around the
working directory; `SNOWOPS_LAB_DIR`; the recorded clone
([details](#which-lab-labctl-uses)). Lab state (active
scenarios and faults, history, progress, snapshots, users) is kept per cluster
in `~/.snowops/state/<cluster>/` (`SNOWOPS_HOME` moves `~/.snowops`). State an
older labctl kept in the clone's `.labctl/` is moved there on first use.

**Build from source** (contributors) - needs Go 1.25+ and Node 22+; see
[CONTRIBUTING](../../../CONTRIBUTING.md#development-setup):

```bash
make cli-build        # builds bin/labctl with the UI embedded
```

## Shell completion

`labctl` can generate shell tab-completion for zsh, bash, fish, and PowerShell.

### zsh

macOS uses zsh by default. To enable completion for every session:

```bash
mkdir -p ~/.zsh/completion
labctl completion zsh > ~/.zsh/completion/_labctl
cat << 'EOF' >> ~/.zshrc
fpath=(~/.zsh/completion $fpath)
autoload -U compinit; compinit
EOF
source ~/.zshrc
```

If `~/.zshrc` does not exist on a fresh macOS installation, creating it with the commands above enables `compinit` and loads `labctl` completions.

To load completion in the current session only:

```bash
source <(labctl completion zsh)
```

### bash

On Linux:

```bash
# Current session
source <(labctl completion bash)

# Every new session
mkdir -p ~/.local/share/bash-completion/completions
labctl completion bash > ~/.local/share/bash-completion/completions/labctl
```

On macOS with Homebrew bash:

```bash
labctl completion bash > $(brew --prefix)/etc/bash_completion.d/labctl
```

### fish and PowerShell

```bash
# fish
labctl completion fish > ~/.config/fish/completions/labctl.fish

# PowerShell
labctl completion powershell | Out-String | Invoke-Expression
```

## Global flags

| Flag | Default | Description |
|---|---|---|
| `--project-dir` | auto-detected | The lab (a snowopslabs clone) to use; see [Install](#install) for how it is found otherwise |
| `-v, --verbose` | `false` | Debug logging: config load, script exec, API calls |
| `--app` | `APP_NAME` | The application scenarios and faults act on for this command. Challenges and learning paths refuse it: they are graded against the default app |
| `--version` | - | Print the build version |

## Configuration

`labctl` reads its settings each time a command starts. This section says
where they come from and which one wins; `config/.env.example` describes every
key. `labctl -v <command>` logs a `config loaded` line with the lab, profile
and cluster it resolved.

### Which lab labctl uses

Without `--project-dir`, the first of these:

1. The nearest clone at or above the working directory: a directory with
   `scenarios/` and `runtimes/`.
2. `SNOWOPS_LAB_DIR`.
3. The clone `install.sh` recorded in `~/.snowops/lab-dir`.

`--project-dir` overrides all three, and is used as given, without checking
that it is a clone: a wrong directory fails later, typically with
`runtime profile "k3d" not found in runtimes/`. A `SNOWOPS_LAB_DIR` that is not
a clone is an error, not a reason to try the recorded clone. Inside a clone,
that clone is used whatever `SNOWOPS_LAB_DIR` says.

### Which value wins

For every key, the first value that is not empty:

1. The environment variable.
2. `.env` in the lab.
3. `runtimes/<PROFILE>/runtime.env`.
4. The built-in default.

`PROFILE` comes from the environment or `.env` (default `k3d`), since it picks
the `runtime.env`. Some defaults follow another key: `CLUSTER_NAME` is
`snowops` (kind's `runtime.env` makes it `snowops-kind`), `DOMAIN_SUFFIX` is
`<CLUSTER_NAME>.localhost` and `INGRESS_CLASS` takes the value of
`INGRESS_PROVIDER`.

An empty value counts as unset wherever it appears. An empty environment
variable is skipped, and an empty line in `.env`, such as `STORAGE_CLASS=`,
leaves the key to the profile, so kind still gets `standard`.

The scripts and Make targets `labctl` runs receive every key from `.env` and
`runtime.env`, resolved the same way.

### Values the cluster recorded

A local runtime records the ingress ports it bound and the domain suffix in
`~/.snowops/clusters/<CLUSTER_NAME>.env`
([runtime profiles](../../runtime-profiles.md)).
For `HTTP_PORT`, `HTTPS_PORT` and `DOMAIN_SUFFIX`, the record wins over `.env`
and `runtime.env`; only an environment variable wins over the record. A new
value in `.env` therefore does not move a cluster that already exists. The
record lasts as long as the cluster: the profile's `down.sh` removes it.

`SNOWOPS_HOME` moves `~/.snowops`, and with it `lab-dir`, `clusters/` and
`state/`.

### Examples

Use kind for one command, without editing `.env`:

```bash
PROFILE=kind labctl -v status
```

The cluster is `snowops-kind` and the URLs end in `snowops-kind.localhost`,
unless `.env` sets `CLUSTER_NAME`, which wins over kind's `runtime.env`.

Use a clone from another directory:

```bash
cd ~
labctl --project-dir ~/src/snowopslabs scenario list
SNOWOPS_LAB_DIR=~/src/snowopslabs labctl scenario list
```

Both use that clone. From inside another clone, only `--project-dir` does.

Find out why a value in `.env` is ignored, for example `HTTP_PORT=80` while
URLs end in `:8080`: an environment variable of the same name, or the
cluster's record, wins over `.env`.

```bash
env | grep '^HTTP_PORT='
cat ~/.snowops/clusters/snowops.env   # the record of the default cluster
```

## Exit status and errors

A command exits `0` when it did what it was asked and `1` otherwise, so it is
safe in scripts and CI. A failing check is a failure: `scenario verify` exits
`1` while any check fails.

- A mistake in how the command was typed (a missing argument, an unknown flag)
  prints the error and that command's usage.
- A command that was typed correctly and then failed prints only the error,
  with what to run next where there is something to run.
- A name that does not exist is answered with the command that lists the names
  that do, such as `see 'labctl scenario list'`.

## Command map

| Page | Commands |
|---|---|
| [Lifecycle](lifecycle.md) | `init` `teardown` `reset` `status` `doctor` `setup-tools` `check` `hosts` `runtime` `lab` `runs` |
| [Applications & services](apps.md) | `app` `service` |
| [Platform](platform.md) | `platform` |
| [Scenarios](scenarios.md) | `scenario` `validate` |
| [Incidents](incidents.md) | `incident` |
| [Traffic](traffic.md) | `traffic` |
| [Comparing stacks](compare.md) | `compare` |
| [Learning & challenges](learning.md) | `learn` `challenge` |
| [Server, metrics & auth](server.md) | `ui` `users` |

## CLI or Make

Both work. Make targets are more granular; the CLI adds scenarios, the web UI
and a unified status view.

| Operation | CLI | Make |
|---|---|---|
| Full setup | `labctl init` | `make init` |
| Build an app | `labctl app build go-api` | `make build APP_NAME=go-api` |
| Deploy an app | `labctl app deploy go-api` | `make deploy APP_NAME=go-api` |
| Platform status | `labctl platform status` | `make platform-status` |
| Activate a scenario | `labctl scenario up observability-sre` | CLI only |
| Web dashboard | `labctl ui` | CLI only |
| Deploy every app | - | `make deploy-all` |
