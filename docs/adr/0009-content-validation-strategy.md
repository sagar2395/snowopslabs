# ADR 0009 — JSON Schemas for authoring, Go loaders for validation

**Status:** Accepted
**Date:** 2026-08-22
**Wave:** W2

## Context

W2 makes all declarative content (scenarios, incidents, learning paths,
challenges) schema-validated, cross-referenced and verifiable. Two things want
to be true at once:

- **Authors and editors** want a machine-readable schema so VS Code (and any
  YAML language server) offers completion and inline errors while typing.
- **`labctl validate` and CI** must fail a bad file with the *file, line and the
  specific reference* that is wrong — not a generic "does not match schema" — and
  must check things a JSON Schema cannot express: that a path's `scenario` ref
  resolves to a real scenario, that a challenge's `useDetectionCheck` is only
  used with an incident, that an asset path stays inside its content directory.

Validating YAML against JSON Schema in Go needs a third-party schema evaluator.
The repo has a no-cgo, minimal-dependency posture (golden rule 1; gosec/
govulncheck gates), and none of the candidate libraries produce the
file+line+reference messages the exit criteria demand.

## Decision

Split the two roles.

- **`sdk/schemas/*.json`** are the authoring reference: one JSON Schema per
  content kind plus a shared `check.schema.json`. They are embedded into the
  binary (`sdk/schemas/schemas.go`) so tooling can fetch them, and a test asserts
  they stay well-formed and free of cut cloud runtimes (ADR-0001).
- **`internal/catalog`** is the authority for validation. Hand-written Go loaders
  parse each kind, report every problem at once with the source file named, run
  cross-reference integrity across the whole catalog, and resolve templates with
  a typed context where an unknown key is an error.

The canonical HTTP-status field is `expectStatus`. `expectedStatus` (used only by
older learning-path content) is accepted as a deprecated alias and reported by
`labctl validate`, so no content breaks on the day the schema lands.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Validate YAML against the JSON Schema at runtime | Adds a dependency, and its errors lack the line/reference precision the exit criteria require |
| Only Go structs, no JSON Schema | Loses editor completion and any external tooling story |
| Generate JSON Schema from the Go structs | Worth doing later; not worth blocking W2 on a generator, and the conditional (`if type == helm then require chart`) rules are awkward to emit |

## Consequences

- The schema and the loader can drift. A test (`sdk/schemas` + catalog golden
  content) guards the obvious cases; full structural equivalence is not enforced.
- Authors get editor help immediately; `labctl validate` gives the precise,
  actionable failures. The two are complementary, not redundant.
- If a schema evaluator with good diagnostics later lands in the toolchain, the
  loaders can call it as a first pass without changing the content or the CLI.
