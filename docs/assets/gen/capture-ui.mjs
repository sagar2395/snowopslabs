// SPDX-License-Identifier: Apache-2.0
//
// Capture screenshots of the embedded labctl web UI.
//
// Prereqs:
//   1. Build the binary:            make cli-build
//   2. Start the UI in another tab: ./bin/labctl ui
//   3. Run this from repo root:     node docs/assets/gen/capture-ui.mjs
//
// It reuses Playwright's Chromium (already a dev dependency of src/ui) and the
// Chromium that Playwright installed. Override the browser with CHROME=/path.
//
// The catalog views (scenarios, incidents, platform, …) render real content
// read from disk, so meaningful screenshots do not require a running cluster.
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const require = createRequire(import.meta.url)
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..')
const { chromium } = require(path.join(repoRoot, 'src/ui/node_modules/playwright-core'))

const OUT = path.join(repoRoot, 'docs/assets')
const BASE = process.env.LABCTL_UI || 'http://localhost:3939'
const CHROME = process.env.CHROME // optional explicit chromium path

// [route, output-basename]
const views = [
  ['scenarios', 'ui-scenarios'],
  ['incidents', 'ui-incidents'],
  ['platform', 'ui-platform'],
  ['dashboard', 'ui-dashboard'],
]

const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {})

async function shoot(theme, list, suffix) {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: theme,
  })
  await ctx.addInitScript((t) => {
    try { localStorage.setItem('snowops-theme', t) } catch {}
  }, theme)
  const page = await ctx.newPage()
  for (const [route, base] of list) {
    await page.goto(`${BASE}/${route}`, { waitUntil: 'networkidle' })
    await page.waitForTimeout(900)
    const file = path.join(OUT, `${base}${suffix}.png`)
    await page.screenshot({ path: file })
    console.log('wrote', path.relative(repoRoot, file))
  }
  await ctx.close()
}

await shoot('dark', views, '')
// One light-theme shot to show the theme system (used by the README <picture>).
await shoot('light', [['scenarios', 'ui-scenarios']], '-light')

await browser.close()
console.log('done')
