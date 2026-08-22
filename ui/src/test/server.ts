// SPDX-License-Identifier: Apache-2.0
//
// MSW server + default handlers.
//
// `handlers` is the happy-path baseline: enough for a component to render
// without every test restating the whole API. A test that needs a different
// response overrides just that endpoint with `server.use(...)`, and the
// override is discarded after the test (see setup.ts).

import { setupServer } from 'msw/node'
import { http, HttpResponse } from 'msw'

/** Base path the UI client uses. Kept here so handlers and client stay aligned. */
export const API = '/api'

export const handlers = [
  http.get(`${API}/auth/me`, () =>
    HttpResponse.json({ authEnabled: false, authenticated: true }),
  ),
  http.get(`${API}/status`, () =>
    HttpResponse.json({
      cluster: { name: 'test-cluster', running: true, nodes: 1, context: 'k3d-test' },
      apps: [],
      platform: [],
    }),
  ),
  http.get(`${API}/runtimes`, () =>
    HttpResponse.json([{ name: 'k3d', active: true, current: true }]),
  ),
  http.get(`${API}/scenarios`, () => HttpResponse.json([])),
  http.get(`${API}/jobs`, () => HttpResponse.json([])),
]

export const server = setupServer(...handlers)

/**
 * jsonError builds the error shape the API returns, so tests assert against the
 * real envelope rather than an invented one. Becomes RFC 7807 problem+json in
 * W5-T02 — this helper is the single place that changes.
 */
export function jsonError(status: number, message: string) {
  return HttpResponse.json({ error: message }, { status })
}
