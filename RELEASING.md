# Releasing SnowOps Labs

Releases are cut by the **lead maintainer** (release authority is reserved - see
[GOVERNANCE.md](GOVERNANCE.md)). This document is the runbook.

## What ships

A release is two things that must match:

- **The lab content**: the tagged commit. Learners clone it
  (`git clone --branch stable`), so what they run, read and edit is the repo
  itself. `LAB_VERSION` at the root names the release; `labctl` warns when it
  and the binary differ.
- **The `labctl` CLI**: a single self-contained binary with the web UI
  embedded, one archive per platform. [`install.sh`](install.sh), run from a
  clone, downloads the archive named by `LAB_VERSION`.

The `stable` branch always points at the latest published release: the README
tells learners to clone it and upgrade by merging `origin/stable`.

`labctl` is cgo-free ([ADR-0002](docs/adr/0002-sqlite-persistence.md)), so all four targets
cross-compile from any host:

- `darwin/amd64`, `darwin/arm64`
- `linux/amd64`, `linux/arm64`

Builds, archives (`.tar.gz`) and a `checksums.txt` (SHA-256) are produced by
[goreleaser](https://goreleaser.com) from
[`src/.goreleaser.yaml`](src/.goreleaser.yaml). The Go module lives under `src/`
(issue #7), so goreleaser runs from there - the CI and release workflows set the
working directory accordingly.

## Versioning

- **SemVer 2.0** for the engine/CLI: `MAJOR.MINOR.PATCH`. The version is stamped
  into the binary at build time (`labctl --version`).
- The **scenario schema** carries its own `apiVersion`
  (`scenario.snowops.net/v2`) and evolves independently; the CLI supports the
  current and previous schema versions.

### Breaking changes

Releases are past 1.0, so the [stability policy](docs/authoring/sdk-stability-policy.md)
applies to the CLI, the content schemas and the public SDK (`pkg/`). Any
breaking change is called out in the release notes.

## TL;DR: cut and publish a release

One command opens the release pull request; merging it tags the release and
builds a **draft** GitHub Release; you review the draft and publish it.

```bash
make release VERSION=1.6.0        # opens the release pull request
# CI green → merge it with "Create a merge commit" or "Rebase and merge", never squash
gh release view v1.6.0 --web      # inspect the draft: notes, archives, checksums
gh release edit v1.6.0 --draft=false   # publish; stable moves to v1.6.0
```

After publishing, the archives appear on the
[Releases page](https://github.com/snowops-oss/snowopslabs/releases), `stable`
moves to the tag, and the [README install steps](README.md#install) give
learners the new release.
The step-by-step version follows.

## How `LAB_VERSION` moves

`main` always carries a development version (`X.Y.Z-dev`); CI fails any change
that leaves it on anything else. `make release VERSION=X.Y.Z` (it runs
[`scripts/release.sh`](scripts/release.sh)) opens a pull request from
`release/vX.Y.Z` with two commits off `main`:

1. `release: vX.Y.Z`: sets `LAB_VERSION` to `X.Y.Z`. This is the commit that
   gets tagged.
2. `chore: start X.<Y+1>.0-dev`: moves `main` on to the next development
   version. Pass `NEXT=X.Y.Z-dev` to choose another.

When the pull request merges, the
[`Tag release` workflow](.github/workflows/tag-release.yaml) finds the commit
whose `LAB_VERSION` is a release that has no tag yet
([`scripts/release-tag.sh`](scripts/release-tag.sh)), tags it `vX.Y.Z`, and
calls the [`Release` workflow](.github/workflows/release.yaml) with that tag. A
squash merge removes the release commit, so the workflow fails and says to run
`make release` again.

## Release steps (maintainer)

1. Before merging the work for a release, accept it on a live lab: the
   [release acceptance](docs/release-acceptance.md) journey
   (`scripts/release-acceptance.sh`) plus a walk of the branch's own changes,
   or the `release-acceptance` skill, which runs both. Then ensure `main` is
   green (the full CI suite, including the `release-config` job that runs
   `goreleaser check` and a snapshot build).
2. Optionally dry-run the artifacts locally (nothing is published):

   ```bash
   cd src    # goreleaser runs where go.mod and .goreleaser.yaml live
   goreleaser release --snapshot --clean --skip=publish
   ls dist/    # four .tar.gz archives + checksums.txt
   ```

3. From a clean checkout, open the release pull request (needs `gh` signed in):

   ```bash
   make release VERSION=X.Y.Z
   ```

4. When CI is green, merge it with **Create a merge commit** or **Rebase and
   merge**, never squash. The `Tag release` workflow tags the release commit
   and the `Release` workflow checks that `LAB_VERSION` matches the tag, runs
   goreleaser, builds the four targets, and opens a GitHub Release **as a
   draft**.
5. Review the draft (notes, artifacts, checksums), then publish it. The
   [`Stable` workflow](.github/workflows/stable.yaml) then fast-forwards the
   `stable` branch to the tag, which is what learners clone and merge. A
   pre-release does not move `stable`.
6. labs.snowops.net picks up the new release's docs, catalog and notes on its
   next daily sync, or straight away if you run that sync by hand (see
   [The website](#the-website)).
7. Announce the release.

## Verifying a download

Reviewers can verify an artifact against the published checksums:

```bash
sha256sum -c checksums.txt   # (shasum -a 256 -c on macOS)
```

## The website

labs.snowops.net is built from the private snowopslabs-web repository. It
publishes the docs, the catalog and the release notes from `stable`, so what
the site says always matches the release people install.

Nothing in this repository drives it. The website's own sync workflow checks
the `stable` commit once a day (06:07 UTC) and, when it has moved, pulls the
docs and catalog, builds the site and deploys it. To publish sooner, run it by
hand after the release:

```bash
gh workflow run sync.yml -R snowopscloud/snowopslabs-web
```

or use Run workflow on the "Sync content from snowopslabs" workflow in the
snowopslabs-web Actions tab.

## Not yet automated

Deferred until after the first feedback round:

- **cosign** signing of artifacts and an **SBOM** (goreleaser supports both; they
  need signing-key and tooling setup).
- **Container image + Helm chart** for in-cluster/team-server mode (the first
  delivery targets the local CLI).
- **Upgrade migration testing** across released versions.

## Hotfixes

Patch releases branch from the release tag, cherry-pick the fix, set
`LAB_VERSION` to the patch version, commit, and push an annotated `vX.Y.Z` tag
on that commit; the `Release` workflow runs on the pushed tag. `make release`
is not used, because the patch does not go through `main`; cherry-pick the fix
onto `main` separately. A patch of the latest release descends from `stable`,
so the `Stable` workflow fast-forwards it. A patch of an older line does not; the
workflow fails and `stable` stays on the newer release, which is what learners
should have.
