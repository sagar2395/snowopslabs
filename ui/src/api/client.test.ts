// SPDX-License-Identifier: Apache-2.0
//
// Contract tests for the API client's failure handling. Every view depends on
// `req()` turning transport problems into messages a human can act on, so its
// error branches matter more than its happy path.
//
// @vitest-environment node
//
// This suite exercises the real fetch path, not the DOM. It must run in the
// `node` environment because jsdom substitutes an AbortController whose signals
// Node's fetch rejects with a TypeError — which would mask every assertion here
// behind a spurious "cannot reach the server". See src/test/setup.ts.

import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { api } from './client'
import { API, jsonError, server } from '../test/server'

describe('api client', () => {
  it('returns the decoded body on success', async () => {
    const status = await api.getAuthStatus()
    expect(status).toEqual({ authEnabled: false, authenticated: true })
  })

  it('sends the request to the /api-prefixed path', async () => {
    let seen = ''
    server.use(
      http.get(`${API}/scenarios`, ({ request }) => {
        seen = new URL(request.url).pathname
        return HttpResponse.json([])
      }),
    )
    await api.listScenarios()
    expect(seen).toBe('/api/scenarios')
  })

  // Each transport failure must produce a distinct, actionable message —
  // "something went wrong" is not a useful thing to show an operator.
  const failures: { name: string; respond: () => Response | Promise<Response>; expect: RegExp }[] = [
    {
      name: 'surfaces the server-supplied error message',
      respond: () => jsonError(409, 'lab is already running'),
      expect: /lab is already running/,
    },
    {
      name: 'falls back to the HTTP status when the error body is not JSON',
      respond: () => new HttpResponse('<html>502</html>', { status: 502 }),
      expect: /502/,
    },
    {
      name: 'reports an invalid (non-JSON) success body',
      respond: () => new HttpResponse('not json', { status: 200 }),
      expect: /invalid response/i,
    },
    {
      name: 'reports an unreachable server rather than a raw TypeError',
      respond: () => HttpResponse.error(),
      expect: /cannot reach the labctl server/i,
    },
  ]

  failures.forEach(({ name, respond, expect: pattern }) => {
    it(name, async () => {
      server.use(http.get(`${API}/scenarios`, respond))
      await expect(api.listScenarios()).rejects.toThrow(pattern)
    })
  })

  it('does not swallow a 404 as an empty list', async () => {
    server.use(http.get(`${API}/scenarios`, () => jsonError(404, 'no such collection')))
    await expect(api.listScenarios()).rejects.toThrow(/no such collection/)
  })
})
