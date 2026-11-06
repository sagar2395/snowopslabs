# Running the lab in GitHub Codespaces

A codespace is a Linux machine GitHub runs for you, with VS Code in the
browser. The lab runs inside it exactly as it does on Linux, so you can try
SnowOps Labs with nothing installed on your own computer.

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/sagar2395/snowopslabs/tree/stable?quickstart=1)

## What you need

- **A GitHub account.** Codespaces bills your account, not this repository.
  GitHub gives personal accounts a free monthly allowance of core hours; the
  lab asks for a 4-core machine, so every hour it runs uses four of them. The
  current allowance is on GitHub's
  [Codespaces billing page](https://docs.github.com/en/billing/managing-billing-for-your-products/managing-billing-for-github-codespaces/about-billing-for-github-codespaces).
- Nothing else. Docker, `labctl`, `kubectl`, `helm` and `k3d` are installed
  inside the codespace.

## Start it

1. Click **Open in GitHub Codespaces** above. It opens the `stable` branch,
   the latest release, which is what `install.sh` needs. With `quickstart=1`,
   GitHub reopens your existing codespace instead of creating a second one.
2. Keep the 4-core machine type and create the codespace.
3. Wait. While VS Code loads, the setup runs `./install.sh` and then
   `labctl init` in the creation log. The first run takes about five to ten
   minutes and ends with the lab's URLs.

A codespace created on `main` (for example from the repository's **Code**
button) is development content with no labctl release, so the setup builds
`labctl` from that clone with `make cli-install` instead of running
`./install.sh`. That adds a few minutes to the first run.

When it finishes, open a terminal (**Terminal → New Terminal**) and run a
scenario, as in the [README](../../README.md#your-first-lab):

```bash
labctl scenario up observability-sre --deploy-prereqs
labctl scenario verify observability-sre
```

## Open the dashboard

```bash
labctl ui
```

Codespaces forwards port 3939 and opens the dashboard in a new browser tab.
If the tab does not open, use the **Ports** panel and click the globe icon on
the `labctl dashboard` row. `labctl ui` also prints that it could not open a
browser; that message is expected here.

## Reach Grafana and other web UIs

On your own machine the lab serves its web UIs at names such as
`http://grafana.snowops.localhost`. In a codespace those names only resolve
inside the codespace, so commands and checks that use them work in the
terminal, and links to them in the dashboard do not open in your browser.

Forward the service to a port instead. For Grafana:

```bash
kubectl -n monitoring port-forward svc/grafana 3000:80
```

Codespaces opens port 3000 in a new tab. Sign in with `admin` / `admin`.
`kubectl get ingress -A` lists the lab's other web UIs (Prometheus, Argo CD
and the rest, as scenarios install them) with the service and port behind
each; forward any of them the same way.

From the terminal, the lab's hostnames work as written:

```bash
curl -s http://grafana.snowops.localhost/api/health
```

## Stopping and coming back

GitHub stops a codespace after it has been idle for a while (30 minutes by
default) and keeps its disk. When you reopen it, run:

```bash
labctl init
```

It restarts the cluster and waits until it is healthy, keeping your apps and
scenarios. A stopped codespace uses no core hours, but its disk counts
against your storage allowance until you delete the codespace.

## Upgrade

The codespace is a clone of this repository on `stable`. Upgrade it as on any
machine ([Using the lab](using-the-lab.md#upgrade)):

```bash
git fetch origin
git merge origin/stable
./install.sh
```

## When something goes wrong

| You see | Do this |
|---|---|
| `labctl: command not found` after the creation log has finished | Setup failed before installing labctl. Open the creation log (**Codespaces: View Creation Log** in the command palette), fix what it names, then run `sh .devcontainer/setup.sh`. |
| `the Docker daemon is not running` | Docker was still starting when setup ran. Run `labctl init` again. |
| A dashboard link to `*.snowops.localhost` does not open | Expected in a codespace. Forward the service with `kubectl port-forward`, as above. |
| `labctl` is not found in a terminal opened before setup finished | Open a new terminal. |

Everything else: [Troubleshooting](../troubleshooting.md), which applies to
the codespace as it does to Linux.
