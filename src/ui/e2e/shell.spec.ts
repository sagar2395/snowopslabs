// SPDX-License-Identifier: Apache-2.0
//
// App-shell journeys. These assert the properties the W6 rebuild must preserve:
// the UI mounts, it survives a backend that is down, and it never renders a
// blank page. Full journeys (run a scenario, watch logs, cancel) land with the
// rebuild — see docs/ROADMAP.md W6-T12.

import { expect, stubApi, test } from './fixtures'

test('mounts and renders the app shell', async ({ page }) => {
  await page.goto('/')
  await expect(page.locator('body')).not.toBeEmpty()
  // The nav is the shell's load-bearing element: if it renders, view selection
  // and the auth gate both resolved.
  await expect(page.getByRole('tab', { name: /dashboard/i })).toBeVisible()
})

test('renders without a reachable backend instead of blanking', async ({ page }) => {
  // Every API call fails at the transport layer, the way it does when the
  // server has been stopped.
  await page.route('**/api/**', (route) => route.abort('failed'))
  await page.goto('/')

  await expect(page.locator('body')).not.toBeEmpty()
  // No unhandled render crash: the error boundary must not have taken over.
  await expect(page.getByText(/this view crashed/i)).toHaveCount(0)
})

test('surfaces a server-side error without crashing the view', async ({ page }) => {
  await stubApi(page, { '/api/status': undefined }) // 404 from the stub table
  await page.goto('/')
  await expect(page.locator('body')).not.toBeEmpty()
  await expect(page.getByText(/this view crashed/i)).toHaveCount(0)
})

test('has no horizontal overflow at a narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 })
  await page.goto('/')
  const overflows = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
  )
  expect(overflows).toBe(false)
})
