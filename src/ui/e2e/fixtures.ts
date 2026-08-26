// SPDX-License-Identifier: Apache-2.0
//
// Shared Playwright fixtures.
//
// `stubApi` intercepts every /api/** request so journeys run without a backend.
// Routes are matched most-specific-first; anything unmatched returns 404 so a
// journey that depends on an unstubbed endpoint fails visibly instead of
// hanging.

import { test as base, type Page } from '@playwright/test'

export type ApiStubs = Record<string, unknown | ((url: URL) => unknown)>

/** Baseline responses: a healthy single-node cluster with nothing installed. */
export const defaultStubs: ApiStubs = {
  '/api/auth/me': { authEnabled: false, authenticated: true },
  '/api/status': {
    cluster: { name: 'e2e-cluster', running: true, nodes: 1, context: 'k3d-e2e' },
    apps: [],
    platform: [],
  },
  '/api/runtimes': [{ name: 'k3d', active: true, current: true }],
  '/api/jobs': [],
  '/api/scenarios': [],
}

export async function stubApi(page: Page, stubs: ApiStubs = {}) {
  const table = { ...defaultStubs, ...stubs }
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url())
    const body = table[url.pathname]
    if (body === undefined) {
      await route.fulfill({ status: 404, json: { error: `unstubbed: ${url.pathname}` } })
      return
    }
    await route.fulfill({ json: typeof body === 'function' ? body(url) : body })
  })
}

/** `test` with the API stubbed to the healthy baseline before each journey. */
export const test = base.extend<{ stubbed: void }>({
  stubbed: [
    async ({ page }, use) => {
      await stubApi(page)
      await use()
    },
    { auto: true },
  ],
})

export { expect } from '@playwright/test'
