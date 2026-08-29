# Releasing SnowOps Labs

Releases are cut by the **lead maintainer** (release authority is reserved — see
[GOVERNANCE.md](GOVERNANCE.md)). This document is the runbook.

## What ships

A release is the `labctl` CLI as a single self-contained binary with the web UI
embedded — one artifact per platform, no separate frontend deploy. It is
cgo-free ([ADR-0002](docs/adr/0002-sqlite-persistence.md)), so all four targets
cross-compile from any host:

- `darwin/amd64`, `darwin/arm64`
- `linux/amd64`, `linux/arm64`

Builds, archives (`.tar.gz`) and a `checksums.txt` (SHA-256) are produced by
[goreleaser](https://goreleaser.com) from
[`src/.goreleaser.yaml`](src/.goreleaser.yaml). The Go module lives under `src/`
(issue #7), so goreleaser runs from there — the CI and release workflows set the
working directory accordingly.

## Versioning

- **SemVer 2.0** for the engine/CLI: `MAJOR.MINOR.PATCH`. The version is stamped
  into the binary at build time (`labctl --version`).
- The **scenario schema** carries its own `apiVersion`
  (`scenario.snowops.net/v2`) and evolves independently; the CLI supports the
  current and previous schema versions.

### Pre-1.0

While pre-1.0, minor versions may include breaking changes, each called out in
the release notes. The public SDK (`pkg/`) stability policy applies from the
first 1.0 release.

## TL;DR — cut and publish a release

Pushing a signed `vX.Y.Z` tag is all it takes: the
[`Release` workflow](.github/workflows/release.yaml) runs goreleaser, builds the
four platform archives + `checksums.txt`, and opens a **draft** GitHub Release.
You review the draft and click **Publish** — then users can download `labctl`
directly instead of building from source.

```bash
# 1. (optional but recommended) dry-run the artifacts locally — publishes nothing
cd src && goreleaser release --snapshot --clean --skip=publish && ls dist/ && cd ..

# 2. tag the release (signed) and push it — this triggers the Release workflow
git tag -s v1.0.0 -m "v1.0.0"
git push origin v1.0.0

# 3. review the draft Release on GitHub, then Publish it (via the web UI or gh):
gh release view v1.0.0 --web        # inspect notes/artifacts/checksums
gh release edit v1.0.0 --draft=false  # publish once it looks right
```

After publishing, the archives appear on the
[Releases page](https://github.com/sagar2395/snowopslabs/releases) and the
download commands in the [README Quickstart](README.md#quickstart) work as-is.
The step-by-step version follows.

## Release steps (maintainer)

1. Ensure `main` is green (the full CI suite, including the `release-config` job
   that runs `goreleaser check` and a snapshot build).
2. Dry-run locally to sanity-check the artifacts (nothing is published):

   ```bash
   cd src    # goreleaser runs where go.mod and .goreleaser.yaml live
   goreleaser release --snapshot --clean --skip=publish
   ls dist/    # four .tar.gz archives + checksums.txt
   ```

3. Tag the release — **signed** (the maintainer holds the signing key):

   ```bash
   git tag -s vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

4. The [`Release` workflow](.github/workflows/release.yaml) runs goreleaser on
   the tag, builds the four targets, and opens a GitHub Release **as a draft**.
5. Review the draft (notes, artifacts, checksums), then publish it.
6. Announce; update docs if needed.

## Verifying a download

Reviewers can verify an artifact against the published checksums:

```bash
sha256sum -c checksums.txt   # (shasum -a 256 -c on macOS)
```

## Not yet automated (post-first-delivery)

Deferred until after the first feedback round — tracked in the W8 tasks:

- **cosign** signing of artifacts and an **SBOM** (goreleaser supports both; they
  need signing-key and tooling setup).
- **Container image + Helm chart** for in-cluster/team-server mode (the first
  delivery targets the local CLI).
- **Upgrade migration testing** across released versions.

## Hotfixes

Patch releases branch from the release tag, cherry-pick the fix, and follow the
same signed-release flow.
