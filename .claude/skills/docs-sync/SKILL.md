---
name: docs-sync
description: "Bring the documentation back in step with the code after any change, and keep the snowopslabs-web site building. Use at the END of every task that touched src/, scenarios/, incidents/, learn/, challenges/, platform/ or config/, and whenever a doc is added, renamed, split or deleted."
user-invocable: true
---

# Keeping the docs true

Documentation is the source of truth, and the website publishes these files
directly. A change is not finished until the docs match it.

## What to update, by what you changed

| You changed | Update |
|---|---|
| A CLI command, flag or its help text | the matching page in `docs/reference/cli/` |
| A `scenario.yaml` field or check type | `docs/reference/scenario-schema.md` |
| A new or removed scenario | `docs/scenarios.md` (the catalog) |
| A new or removed fault | the table in `incidents/README.md` |
| A learning path or challenge | the table in `learn/README.md` or `challenges/README.md` |
| A platform component or chart pin | `docs/runbooks/R05-platform-components.md` |
| Anything metrics, logs or traces | `docs/runbooks/R13-observability-pipeline.md` |
| An API route or convention | the REST section of the relevant `docs/reference/cli/` page |
| A structural or notable decision | a new ADR under `docs/adr/`, listed in `docs/adr/README.md` |

If the change makes an existing sentence false, fix that sentence. Stale docs
are worse than missing ones, because agents are told to trust them.

## Run the gates

```bash
make docs-check     # relative links resolve; every published path exists
```

`docs-links` also flags a documentation path cited in backticks that no longer
exists — prose goes stale that way silently. ADRs are exempt, since they record
files a decision deleted.

## If you added, renamed, split or deleted a doc

The website's manifest names each published file by path. A rename with no
matching manifest edit drops the page from the site with no error.

1. Edit `scripts/docs-manifest.mjs` in the **snowopslabs-web** repository —
   `src` (path in this repo), `slug` (URL under `/docs`), `title`, `group`,
   `desc`.
2. Regenerate and verify there:
   ```bash
   npm run sync -- --local
   npm run lint
   npm run build
   ```
3. Commit the generated `content/` — it is committed, and the deployed site
   keeps the old data until you do.
4. Grep the site's own source for hardcoded links to the old slug:
   `src/config/site.ts` and `src/app/` both carry some.

`make docs-check` catches a missing manifest path from this side, but it cannot
know about a hardcoded link in the site's components. Check both.

## Writing style

These pages are read by users of the product, not only by maintainers.

- Lead with what the reader does, then the detail.
- One idea per sentence; no wave, task or PR numbers anywhere.
- Every command shown must run as written.
- Mark anything not yet built as **Planned**. Never document an intention as if
  it ships — especially a security property.

## Before you finish

- [ ] Every doc the change affects is updated in the same change.
- [ ] `make docs-check` passes.
- [ ] Manifest updated and the site rebuilt, if a doc moved.
- [ ] No task or ticket numbers introduced.
