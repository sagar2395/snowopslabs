# Using the lab

Day-to-day use of a lab you have already built: changing what it installs,
bringing it back after a reboot, upgrading, configuring and uninstalling.
Building it the first time is in the [README](../../README.md#install) and
the guide for your OS.

## Everyday commands

| You want to… | Run |
|---|---|
| Bring the lab back after a reboot | `labctl init`: it starts Docker, restarts the cluster and waits for it, keeping your apps and scenarios |
| See what is running and whether it is healthy | `labctl status` (or the dashboard) |
| Run several scenarios at once | Start them one after another; labctl stops you, with the fix, if Docker has no room left |
| Start again from a clean cluster | `labctl reset` |
| Upgrade | See [Upgrade](#upgrade) below |
| Check your machine | `labctl doctor` |

Every command and flag is in the [CLI reference](../reference/cli/index.md).

## Lab URLs

Lab URLs look like `http://grafana.snowops.localhost`: the service, the lab's
name, then `localhost`. Every browser resolves `*.localhost` to your own
machine, so they work with no hosts-file edits, on macOS, Linux, and in a
Windows browser for a lab running in WSL. If port 80 is taken, the lab uses a
free port and every URL it prints includes it.

## Change what the lab installs

Open your clone in your editor. What a scenario installs is in
`scenarios/<name>/` (manifests, scripts, checks), each platform component's
Helm values in `platform/<category>/<provider>/values.yaml`, and each fault in
`incidents/<name>/`. `labctl scenario info <name>` lists what a scenario uses.

Work on a branch of your own, so upgrades merge cleanly and you can always get
back to the original:

```bash
git switch -c my-experiments
```

| You want to… | Do |
|---|---|
| Try a change on the live cluster | `kubectl edit` / `kubectl apply`; `labctl scenario down` then `up` puts the scenario back |
| Change how something is built | Edit its manifest or values file, then `labctl platform up <component>` or `labctl scenario up <name>` |
| See what you changed | `git diff` |
| Undo it | `git restore .`, then `labctl reset` if the cluster needs rebuilding |
| Keep it | `git commit` |
| Check your edits are valid | `labctl validate` |

## Bring your own app

Add `apps/<name>/` with an `app.env`; it then shows up in `labctl app list`
and can be bound to scenarios and faults with `--app <name>`. The contract an
app has to meet is in [apps](../reference/cli/apps.md).

## Configuration

`labctl` reads `.env` in your clone (`install.sh` creates it from
`config/.env.example`):

```bash
LAB_CPUS=2          # size colima is started at (macOS)
LAB_MEMORY=4
INGRESS_PROVIDER=traefik
METRICS_PROVIDER=prometheus
```

Every key is described in `config/.env.example`. An environment variable wins
over `.env`, and `.env` over the profile's `runtime.env`; the full order, and
how labctl finds the lab, are in
[Configuration](../reference/cli/index.md#configuration).

## Upgrade

From your clone, on your own branch:

```bash
git fetch origin
git merge origin/stable      # your changes are kept; git stops on a conflict
./install.sh                 # the labctl that matches
```

If you have uncommitted changes, commit or stash them first. When labctl and
your clone come from different releases, every command says so and prints the
fix. Each [release](https://github.com/sagar2395/snowopslabs/releases) says
whether anything needs rebuilding after the merge.

## Uninstall

```bash
labctl teardown                      # delete the cluster
labctl hosts remove                  # only if you added hosts entries for an older lab
rm -rf ~/.snowops ~/.local/bin/labctl
rm -rf snowopslabs                   # your clone, once you no longer need your changes
colima delete --data                 # macOS only, if you no longer need colima's VM and its images
```

`~/.snowops` holds labctl's state for the cluster (active scenarios, history,
progress); it lives outside your clone, so deleting or re-cloning the lab
loses none of it.
