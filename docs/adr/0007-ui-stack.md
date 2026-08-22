# ADR 0007 — UI stack: React Router, TanStack Query, Tailwind, Radix

**Status:** Accepted
**Date:** 2026-07-26
**Wave:** W6

## Context

The v1 UI was 2,640 lines of hand-rolled React with:

- **no router** — the active tab lived in `useState`, so there were no deep
  links, browser back did nothing, and a refresh dumped you on the dashboard,
- **no data layer** — every view hand-wrote fetch, loading and error handling,
  with a `refreshTick` counter threaded through props to force refetches,
- **no design system** — a single hand-written stylesheet, one theme,
- **no tests at all.**

The product requirement is a UI that is engaging, descriptive and easy to use.
That is not reachable by patching this.

## Decision

Rebuild on: **React 18 + TypeScript (strict) + Vite**, **React Router** for real
URLs, **TanStack Query** for server state, **Tailwind** with design tokens for
styling, and **Radix primitives** for accessible dialogs, menus, tabs and
tooltips. Tested with **Vitest + Testing Library + MSW** at the component layer
and **Playwright** for journeys.

The built SPA continues to be embedded into the Go binary — one artifact.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Incremental polish of v1 | Keeps the structural limits (no routing, no caching) that cause the UX problems |
| Next.js | SSR and a Node runtime buy nothing for an embedded SPA served by a Go binary; adds deployment weight |
| Component kit (MUI, Chakra, Ant) | Heavy, opinionated visual identity that is hard to make feel like an operations console; Radix gives behaviour and a11y without imposing looks |
| CSS Modules / vanilla-extract | Fine choices; Tailwind wins on iteration speed and on making a token system enforceable in review |
| Redux / Zustand for server state | The hard problem here is server-state caching and invalidation, which TanStack Query solves directly. Local UI state stays in components |

## Consequences

- **Easier:** deep links work, back works, refresh preserves state. Caching,
  retry and invalidation-on-run-completion are configured once, not per view.
  Accessibility comes from Radix rather than from remembering ARIA attributes.
- **Harder:** more dependencies to keep current. Mitigated by Dependabot and the
  licence scan gate.
- Design tokens must be defined before views are built, otherwise Tailwind
  becomes inconsistent utility soup. Tokens are W6-T03, ahead of every view task.
- MSW mocks at the network layer, so component tests exercise the real fetch
  path and catch serialisation mistakes that module stubbing would hide.
