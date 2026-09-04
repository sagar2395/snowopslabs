# CLI reference

`labctl` is the command-line interface for SnowOps Labs. It builds the lab,
runs scenarios, injects faults and grades your fix.

## Install

**Download a release** — no Go or Node needed. Pick the archive for your
OS/arch from the [Releases page](https://github.com/sagar2395/snowopslabs/releases).
macOS, Linux and Windows (inside WSL2) use the same commands:

```bash
VERSION=1.0.0                                          # from the Releases page
OS=$(uname -s | tr '[:upper:]' '[:lower:]')            # darwin | linux
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
BASE="https://github.com/sagar2395/snowopslabs/releases/download/v${VERSION}"

curl -fsSL "${BASE}/labctl_${VERSION}_${OS}_${ARCH}.tar.gz" | tar xz labctl
sudo mv labctl /usr/local/bin/
labctl --version
```

**Build from source** — needs Go 1.25+ and Node 22+:

```bash
make cli-build        # builds bin/labctl with the UI embedded
make cli-install      # builds and copies onto your PATH
```

The [Quickstart](../../../README.md#quickstart) has the per-OS walkthrough,
including checksum verification.

## Global flags

| Flag | Default | Description |
|---|---|---|
| `--project-dir` | auto-detected | Project root directory |
| `-v, --verbose` | `false` | Debug logging: config load, script exec, API calls |
| `--version` | — | Print the build version |

## Command map

| Page | Commands |
|---|---|
| [Lifecycle](lifecycle.md) | `init` `teardown` `reset` `status` `doctor` `setup-tools` `check` `hosts` `runtime` `lab` `runs` |
| [Applications & services](apps.md) | `app` `service` |
| [Platform](platform.md) | `platform` |
| [Scenarios](scenarios.md) | `scenario` `validate` |
| [Incidents](incidents.md) | `incident` |
| [Traffic](traffic.md) | `traffic` |
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
| Deploy every app | — | `make deploy-all` |
