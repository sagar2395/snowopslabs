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

import { beforeEach, describe, expect, it } from 'vitest'
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

// Every method on `api` is a thin binding of an HTTP verb to a URL. A wrong verb
// or a mistyped path is invisible to the type-checker and only shows up as a
// 404/405 at runtime, so we assert the verb and the exact, correctly-encoded
// path for each one. A catch-all handler records the request and answers 200 so
// the call resolves regardless of the declared response type.
describe('api client — every endpoint binds the right verb and path', () => {
  let seen: { method: string; path: string }

  beforeEach(() => {
    seen = { method: '', path: '' }
    server.use(
      http.all('*', ({ request }) => {
        seen = { method: request.method, path: new URL(request.url).pathname }
        return HttpResponse.json({})
      }),
    )
  })

  const cases: { name: string; call: () => Promise<unknown>; method: string; path: string }[] = [
    { name: 'getAuthStatus', call: () => api.getAuthStatus(), method: 'GET', path: '/api/auth/me' },
    { name: 'login', call: () => api.login('u', 'p'), method: 'POST', path: '/api/auth/login' },
    { name: 'logout', call: () => api.logout(), method: 'POST', path: '/api/auth/logout' },
    { name: 'getStatus', call: () => api.getStatus(), method: 'GET', path: '/api/status' },
    { name: 'getDashboards', call: () => api.getDashboards(), method: 'GET', path: '/api/dashboards' },
    { name: 'getJobs', call: () => api.getJobs(), method: 'GET', path: '/api/jobs' },
    { name: 'listApps', call: () => api.listApps(), method: 'GET', path: '/api/apps' },
    { name: 'buildApp', call: () => api.buildApp('go-api'), method: 'POST', path: '/api/apps/go-api/build' },
    { name: 'deployApp', call: () => api.deployApp('go-api'), method: 'POST', path: '/api/apps/go-api/deploy' },
    { name: 'destroyApp', call: () => api.destroyApp('go-api'), method: 'POST', path: '/api/apps/go-api/destroy' },
    { name: 'getPlatform', call: () => api.getPlatform(), method: 'GET', path: '/api/platform' },
    { name: 'platformUp', call: () => api.platformUp(), method: 'POST', path: '/api/platform/up' },
    { name: 'platformDown', call: () => api.platformDown(), method: 'POST', path: '/api/platform/down' },
    // catSegment sends only the last segment of a nested category.
    { name: 'componentUp', call: () => api.componentUp('monitoring/metrics', 'prometheus'), method: 'POST', path: '/api/platform/component/metrics/prometheus/up' },
    { name: 'componentDown', call: () => api.componentDown('monitoring/metrics', 'prometheus'), method: 'POST', path: '/api/platform/component/metrics/prometheus/down' },
    { name: 'listScenarios', call: () => api.listScenarios(), method: 'GET', path: '/api/scenarios' },
    { name: 'getScenario', call: () => api.getScenario('obs'), method: 'GET', path: '/api/scenarios/obs' },
    { name: 'scenarioUp', call: () => api.scenarioUp('obs'), method: 'POST', path: '/api/scenarios/obs/up' },
    { name: 'scenarioDown', call: () => api.scenarioDown('obs'), method: 'POST', path: '/api/scenarios/obs/down' },
    { name: 'listRuntimes', call: () => api.listRuntimes(), method: 'GET', path: '/api/runtimes' },
    { name: 'activateRuntime', call: () => api.activateRuntime('k3d'), method: 'POST', path: '/api/runtimes/k3d/activate' },
    { name: 'deactivateRuntime', call: () => api.deactivateRuntime('k3d'), method: 'POST', path: '/api/runtimes/k3d/deactivate' },
    { name: 'listLearnPaths', call: () => api.listLearnPaths(), method: 'GET', path: '/api/learn/paths' },
    { name: 'getLearnPath', call: () => api.getLearnPath('sre'), method: 'GET', path: '/api/learn/paths/sre' },
    { name: 'getLearnProgress', call: () => api.getLearnProgress('sre'), method: 'GET', path: '/api/learn/paths/sre/progress' },
    { name: 'startLearnPath', call: () => api.startLearnPath('sre'), method: 'POST', path: '/api/learn/paths/sre/start' },
    { name: 'completeLearnModule', call: () => api.completeLearnModule('sre', 2), method: 'POST', path: '/api/learn/paths/sre/complete' },
    { name: 'listIncidents', call: () => api.listIncidents(), method: 'GET', path: '/api/incidents' },
    { name: 'getIncidentStatus', call: () => api.getIncidentStatus(), method: 'GET', path: '/api/incidents/status' },
    { name: 'getIncidentHistory', call: () => api.getIncidentHistory(), method: 'GET', path: '/api/incidents/history' },
    { name: 'injectIncident', call: () => api.injectIncident('cpu'), method: 'POST', path: '/api/incidents/cpu/inject' },
    { name: 'injectRandomIncident', call: () => api.injectRandomIncident(), method: 'POST', path: '/api/incidents/inject-random' },
    { name: 'resolveIncident', call: () => api.resolveIncident(), method: 'POST', path: '/api/incidents/resolve' },
    { name: 'nextIncidentHint', call: () => api.nextIncidentHint(), method: 'POST', path: '/api/incidents/hint' },
    { name: 'listChallenges', call: () => api.listChallenges(), method: 'GET', path: '/api/challenges' },
    { name: 'getChallengeStatus', call: () => api.getChallengeStatus(), method: 'GET', path: '/api/challenges/status' },
    { name: 'getChallengeHistory', call: () => api.getChallengeHistory(), method: 'GET', path: '/api/challenges/history' },
    { name: 'getResults', call: () => api.getResults(), method: 'GET', path: '/api/results' },
    { name: 'getResultsByKind', call: () => api.getResultsByKind('scenario'), method: 'GET', path: '/api/results/scenario' },
    { name: 'getLeaderboard', call: () => api.getLeaderboard(), method: 'GET', path: '/api/leaderboard' },
    { name: 'getTraffic', call: () => api.getTraffic(), method: 'GET', path: '/api/traffic' },
    { name: 'startTraffic', call: () => api.startTraffic({ profile: 'steady', rps: 10 }), method: 'POST', path: '/api/traffic/start' },
    { name: 'stopTraffic', call: () => api.stopTraffic(), method: 'POST', path: '/api/traffic/stop' },
    { name: 'listRuns', call: () => api.listRuns(), method: 'GET', path: '/api/runs' },
    { name: 'getRun', call: () => api.getRun('r1'), method: 'GET', path: '/api/runs/r1' },
    { name: 'getRunLogs', call: () => api.getRunLogs('r1'), method: 'GET', path: '/api/runs/r1/logs' },
    { name: 'cancelRun', call: () => api.cancelRun('r1'), method: 'POST', path: '/api/runs/r1/cancel' },
  ]

  cases.forEach(({ name, call, method, path }) => {
    it(`${name} → ${method} ${path}`, async () => {
      await call()
      expect(seen).toEqual({ method, path })
    })
  })

  it('percent-encodes untrusted path segments', async () => {
    await api.getScenario('a/b c')
    expect(seen.path).toBe('/api/scenarios/a%2Fb%20c')
  })
})
