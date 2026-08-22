# RFCs

Some changes are too big or too far-reaching to settle in a pull request:
anything touching the engine, the public SDK (`pkg/`), the scenario schema, the
HTTP API, or the project's licensing and governance. For those, open an **RFC**
first so the design is agreed before the code is written.

## Process

1. Copy `0000-template.md` to `NNNN-short-title.md` (next free number).
2. Fill it in — problem, proposed design, alternatives considered, and the
   impact on existing content and compatibility.
3. Open it as a pull request. Discussion happens on the PR.
4. A maintainer accepts, requests changes, or declines. An accepted RFC is
   merged and becomes the reference for the implementation work.

Small, obvious changes don't need an RFC — just open a PR. When in doubt, ask in
an issue first.

## Template

See [`0000-template.md`](0000-template.md).
