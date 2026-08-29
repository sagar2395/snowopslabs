// SPDX-License-Identifier: Apache-2.0
//
// Generate the SnowOps Labs brand assets (logo mark, horizontal lockups,
// favicon, social preview) as SVG, then render the PNG variants.
//
// One source of truth for the palette and the snowflake glyph, so every asset
// stays consistent with the web UI (src/ui/src/index.css tokens).
//
//   node docs/assets/gen/brand.mjs          # writes SVGs + renders PNGs
//   CHROME=/path/to/chromium node …         # override the browser
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import fs from 'node:fs'
import path from 'node:path'

const require = createRequire(import.meta.url)
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..')
const { chromium } = require(path.join(repoRoot, 'src/ui/node_modules/playwright-core'))
const CHROME = process.env.CHROME

const BRAND = path.join(repoRoot, 'docs/assets/brand')
const PUBLIC = path.join(repoRoot, 'src/ui/public')
fs.mkdirSync(BRAND, { recursive: true })
fs.mkdirSync(PUBLIC, { recursive: true })

// ── Palette (mirrors src/ui/src/index.css) ────────────────────────────────
const ACCENT = '#38bdf8'
const ACCENT2 = '#818cf8'
const SNOW = '#f8fafc'
const INK = '#0f172a'      // wordmark on light backgrounds
const BG0 = '#05070d'
const BG1 = '#0f1729'
const FONT = "'DejaVu Sans Mono','SFMono-Regular',ui-monospace,monospace"

// ── The snowflake mark: a rounded gradient tile + a 6-fold snowflake ───────
// `id` must be unique per document when several marks share a page.
function mark(id, { tile = true } = {}) {
  const arms = [0, 60, 120, 180, 240, 300]
    .map((d) => `<use href="#arm-${id}" transform="rotate(${d} 32 32)"/>`)
    .join('')
  return `
    <defs>
      <linearGradient id="tile-${id}" x1="0" y1="0" x2="64" y2="64" gradientUnits="userSpaceOnUse">
        <stop offset="0" stop-color="${ACCENT}"/><stop offset="1" stop-color="${ACCENT2}"/>
      </linearGradient>
      <g id="arm-${id}" fill="none" stroke="${SNOW}" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M32 32 V10"/>
        <path d="M32 21 l4.6 -3.4 M32 21 l-4.6 -3.4"/>
        <path d="M32 15 l3.2 -2.4 M32 15 l-3.2 -2.4"/>
      </g>
    </defs>
    ${tile ? `<rect x="0" y="0" width="64" height="64" rx="15" fill="url(#tile-${id})"/>` : ''}
    <g opacity="0.96">${arms}</g>
    <circle cx="32" cy="32" r="3.1" fill="${SNOW}"/>`
}

function svg(w, h, body, label) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${w} ${h}" width="${w}" height="${h}"` +
    (label ? ` role="img" aria-label="${label}"` : '') + `>${body}</svg>\n`
}

// ── Assets ────────────────────────────────────────────────────────────────
const files = {}

// 1. Icon mark (square tile).
files[`${BRAND}/mark.svg`] = svg(64, 64, mark('m'), 'SnowOps Labs')

// 2. Favicon — the same mark, served by the UI.
files[`${PUBLIC}/favicon.svg`] = svg(64, 64, mark('fav'), 'SnowOps Labs')

// 3. Horizontal lockup: mark + wordmark. Two colourways for light/dark grounds.
function lockup(id, wordFill) {
  const markBox = `<g transform="translate(0 4) scale(0.84)">${mark(id)}</g>`
  const word =
    `<text x="72" y="41" font-family="${FONT}" font-size="30" font-weight="700" letter-spacing="-0.5">` +
    `<tspan fill="${ACCENT}">SnowOps</tspan><tspan fill="${wordFill}"> Labs</tspan></text>`
  return svg(320, 64, markBox + word, 'SnowOps Labs')
}
files[`${BRAND}/logo-dark.svg`] = lockup('ld', SNOW)   // light wordmark → dark bg
files[`${BRAND}/logo-light.svg`] = lockup('ll', INK)   // dark wordmark → light bg

// 4. Social preview banner (GitHub recommends 1280×640).
const dots = Array.from({ length: 26 }, () => {
  const x = Math.round(Math.random() * 1280), y = Math.round(Math.random() * 640)
  const r = (Math.random() * 1.6 + 0.6).toFixed(1), o = (Math.random() * 0.06 + 0.02).toFixed(3)
  return `<circle cx="${x}" cy="${y}" r="${r}" fill="${SNOW}" opacity="${o}"/>`
}).join('')
const banner =
  `<defs><linearGradient id="bg" x1="0" y1="0" x2="1280" y2="640" gradientUnits="userSpaceOnUse">` +
  `<stop offset="0" stop-color="${BG0}"/><stop offset="1" stop-color="${BG1}"/></linearGradient></defs>` +
  `<rect width="1280" height="640" fill="url(#bg)"/>${dots}` +
  // giant faint snowflake, bleeding off the right edge
  `<g transform="translate(980 320) scale(9)" opacity="0.10">${mark('big', { tile: false })}` +
  `</g>` +
  `<g transform="translate(120 232)"><g transform="scale(1.7)">${mark('sp')}</g></g>` +
  `<text x="240" y="300" font-family="${FONT}" font-size="60" font-weight="700" letter-spacing="-1" fill="${SNOW}">` +
  `<tspan fill="${ACCENT}">SnowOps</tspan> Labs</text>` +
  `<text x="242" y="352" font-family="${FONT}" font-size="26" font-weight="400" fill="#94a3b8">` +
  `A Kubernetes platform-engineering simulator</text>` +
  `<text x="242" y="404" font-family="${FONT}" font-size="21" font-weight="400" fill="#64748b">` +
  `Stand it up · break it on purpose · fix it · get graded</text>`
files[`${BRAND}/social-preview.svg`] = svg(1280, 640, banner)

for (const [p, c] of Object.entries(files)) fs.writeFileSync(p, c)
console.log('wrote', Object.keys(files).length, 'SVGs')

// ── Render PNGs ────────────────────────────────────────────────────────────
const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {})
async function png(svgFile, outPng, w, bg) {
  const src = fs.readFileSync(svgFile, 'utf8')
  const [, vw, vh] = src.match(/viewBox="0 0 ([\d.]+) ([\d.]+)"/)
  const h = Math.round(w * vh / vw)
  const html = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0}` +
    `#b{width:${w}px;height:${h}px;${bg ? `background:${bg};` : ''}}#b svg{width:100%;height:100%;display:block}</style>` +
    `<div id="b">${src}</div>`
  const page = await browser.newPage({ deviceScaleFactor: 2 })
  await page.setContent(html)
  await page.waitForTimeout(120)
  await page.locator('#b').screenshot({ path: outPng, omitBackground: !bg })
  await page.close()
  console.log('wrote', path.relative(repoRoot, outPng), `${w}×${h}`)
}

await png(`${BRAND}/mark.svg`, `${BRAND}/mark.png`, 256)
await png(`${BRAND}/mark.svg`, `${PUBLIC}/favicon-32.png`, 32)
await png(`${BRAND}/mark.svg`, `${PUBLIC}/apple-touch-icon.png`, 180, BG1)
await png(`${BRAND}/logo-dark.svg`, `${BRAND}/logo-dark.png`, 640)
await png(`${BRAND}/logo-light.svg`, `${BRAND}/logo-light.png`, 640)
await png(`${BRAND}/social-preview.svg`, `${BRAND}/social-preview.png`, 1280)
await browser.close()
console.log('done')
