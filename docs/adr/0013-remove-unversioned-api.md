# ADR 0013 — Remove the unversioned `/api` alias

**Status:** Accepted
**Date:** 2026-09-04

## Context

[ADR-0006](0006-api-conventions.md) introduced `/api/v2` and kept the
unversioned `/api` mounted alongside it as a compatibility alias, so the
embedded UI could keep working while the new conventions landed. The two
prefixes shared one route table and diverged only in two places, both selected
by an API-version tag on the request context:

- errors — `application/problem+json` under `/api/v2`, the legacy
  `{error, code}` envelope under `/api`;
- list collections — the `{items, nextCursor}` cursor envelope under `/api/v2`,
  a bare array under `/api`.

The alias outlived its purpose. The UI never migrated, so it was still the
only consumer of `/api` — meaning the product's own client was the one thing
holding the legacy contract open, and no external user was relying on it. Two
live response shapes for every collection and every error is a standing source
of drift: a handler could satisfy its tests under one envelope and be wrong
under the other, and the documentation described the legacy paths as if they
were the API.

## Decision

Serve **`/api/v2` only**. Delete the unversioned `/api` subrouter, the version
context tag and the two branch points that read it, and migrate the embedded
React client to `/api/v2`.

A request to `/api/...` is no longer a route. It falls through to the SPA
handler and receives the application shell, so an old client gets HTML rather
than a plausible-looking JSON body it might parse as success.

The client reads `detail` (falling back to `title`) from problem+json for its
error messages, and reads paginated collections whole by following `nextCursor`
— the four list endpoints are `/scenarios`, `/challenges`, `/learn/paths` and
`/runs`. Paging is transparent to the views, which still render a full list.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep `/api` as a 301 to `/api/v2` | Preserves the version machinery and both branch points — the drift risk this decision exists to remove. A redirect also cannot fix the response shape, so a legacy client would follow it and then fail to parse the envelope. |
| Keep the alias until a major release | It has no users. The product's own UI was the only consumer, and it is migrated in the same change. |
| Migrate the UI but leave `/api` mounted | Leaves an untested, undocumented surface serving a contract nothing exercises — the state most likely to rot. |

## Consequences

- One response shape per endpoint. A handler cannot be right under one envelope
  and wrong under another.
- Views that render a list now page transparently. The run console previously
  received every run in one bare array; it now follows the cursor, so it is no
  longer silently capped by whatever the server chose to return.
- Any third-party script written against `/api` breaks. That is intended, and it
  breaks loudly: it gets HTML, not a stale success body.
- `ErrorResponse`, `apiVersionMiddleware`, `apiVersionFrom` and the `apiV1`
  constant are gone. A future `/api/v3` mounts a second prefix rather than
  branching inside handlers.

Supersedes the alias described in the implementation status of
[ADR-0006](0006-api-conventions.md); the rest of that ADR stands.
