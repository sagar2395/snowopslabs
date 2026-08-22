# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, use one of:

- **GitHub Private Vulnerability Reporting** — the "Report a vulnerability" button
  under this repository's **Security** tab (preferred). _Maintainer: enable this
  in repo settings — manual action E2._
- Email **security@snowops.net** with details. _Maintainer: set this to a real,
  monitored alias before launch — manual action E2._

Please include: a description, reproduction steps, affected version/commit, and
impact. If you have a suggested fix, include it.

## What to expect

- **Acknowledgement** within 3 business days.
- An initial assessment and severity rating shortly after.
- Coordinated disclosure: we'll agree on a timeline and credit you (if you wish)
  once a fix is available.

## Scope

In scope: the SnowOps Labs engine, the `labctl` CLI, the SDK (`pkg/`), and
first-party platform modules/scenarios in this repository.

Out of scope: vulnerabilities in the third-party tools SnowOps Labs orchestrates
(kubectl, helm, Istio, Vault, Prometheus, etc.) — report those upstream. Also out
of scope: issues that require a pre-compromised cluster or that are inherent to
running attacker-supplied content. Scenarios and incidents run shell scripts
against your cluster by design, so content loaded from an external root
(`SNOWOPS_CONTENT_PATH`) is trusted code — only point it at sources you
trust. See `docs/adr/0008-content-extensibility-seam.md`.

## Supported versions

Until the first stable release, only the latest `main` is supported. A formal
supported-versions matrix will be published with the first tagged release (see
[RELEASING.md](RELEASING.md)).
