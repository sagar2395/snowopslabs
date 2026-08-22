// SPDX-License-Identifier: Apache-2.0
//
// Global test setup for the component layer.
//
// The MSW server is started once for the whole run. Handlers are reset between
// tests so per-test overrides cannot leak, and unhandled requests error rather
// than silently hitting the network — a test that forgets to mock an endpoint
// must fail loudly, not pass by accident.

import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { server } from './server'

// The API client (src/api/client.ts) issues fetches against relative `/api`
// paths and wraps each in an AbortController for its timeout. Two things have
// to hold for that to work under test:
//
//   - MSW resolves a relative request URL against `location`, so a file running
//     in the `node` environment (which has no DOM, hence no `location`) needs
//     one supplied. jsdom already provides it, so only fill the gap.
//   - Node's own `fetch` brand-checks the AbortSignal it is handed. jsdom
//     replaces `AbortController` with an implementation whose signals fail that
//     check, so the fetch-contract tests (client.test.ts) run in `node`, where
//     the native AbortController is intact.
if (typeof (globalThis as { location?: unknown }).location === 'undefined') {
  ;(globalThis as { location?: unknown }).location = new URL('http://localhost:3000/')
}

beforeAll(() => {
  server.listen({ onUnhandledRequest: 'error' })
})

afterEach(() => {
  cleanup()
  server.resetHandlers()
})

afterAll(() => {
  server.close()
})
