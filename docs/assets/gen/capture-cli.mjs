// SPDX-License-Identifier: Apache-2.0
//
// Render animated terminal GIFs of real `labctl` command output.
//
// Prereqs:
//   1. Build the binary:  make cli-build          (produces ./bin/labctl)
//   2. Install two encode-only deps (pure JS, no native build):
//        npm --prefix docs/assets/gen install gifenc pngjs
//   3. Run from repo root: node docs/assets/gen/capture-cli.mjs
//
// It executes labctl for real, captures stdout, replays it in a styled terminal
// frame-by-frame with Playwright's Chromium, then encodes a GIF with gifenc.
// Override the browser with CHROME=/path/to/chromium.
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const require = createRequire(import.meta.url)
const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(here, '../../..')
const { chromium } = require(path.join(repoRoot, 'src/ui/node_modules/playwright-core'))
const { PNG } = require(path.join(here, 'node_modules/pngjs'))
const { GIFEncoder, quantize, applyPalette } = require(path.join(here, 'node_modules/gifenc'))

const OUT = path.join(repoRoot, 'docs/assets')
const LABCTL = path.join(repoRoot, 'bin/labctl')
const CHROME = process.env.CHROME
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'snowops-cli-'))

const run = (args) =>
  execFileSync(LABCTL, args, { cwd: repoRoot, encoding: 'utf8' }).replace(/\s+$/, '').split('\n')

// --- storyboards ----------------------------------------------------------
const cmd = (text) => ({ t: 'cmd', text })
const out = (lines) => ({ t: 'out', lines })
const hold = (f) => ({ t: 'hold', f })
const note = (text) => ({ t: 'comment', text })

const GIFS = {
  'demo-cli': {
    cols: 108, rows: 34, width: 1000,
    scenes: [
      note('# 1. See the scenario catalog — real production-shaped playgrounds'),
      cmd('labctl scenario list'), out(run(['scenario', 'list'])), hold(20),
      note('# 2. Break something on purpose — reversible, realistic faults'),
      cmd('labctl incident list'), out(run(['incident', 'list'])), hold(20),
      note('# 3. Every declarative file is machine-checked'),
      cmd('labctl validate'), out(run(['validate'])), hold(28),
    ],
  },
  'demo-scenario-info': {
    cols: 120, rows: 40, width: 1100,
    scenes: [
      note('# Inspect exactly what a scenario installs, its objectives and checks'),
      cmd('labctl scenario info observability-sre'),
      out(run(['scenario', 'info', 'observability-sre'])), hold(36),
    ],
  },
}

// --- frame model ----------------------------------------------------------
const CHAR_PER_FRAME = 3
const FPS = 12
const PROMPT =
  '<span class="u">you</span><span class="at">@</span><span class="h">snowops-labs</span>' +
  '<span class="c">:</span><span class="p">~/snowopslabs</span><span class="d">$</span> '

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

function colorLine(raw) {
  const s = esc(raw)
  if (/^\s*✓/.test(raw)) return `<span class="ok">${s}</span>`
  if (/^\s*✗/.test(raw)) return `<span class="err">${s}</span>`
  if (/^[A-Z][A-Z0-9 &/()-]+$/.test(raw.trim()) && raw.trim().length < 60) return `<span class="hdr">${s}</span>`
  if (/^(NAME|PATH)\s{2,}/.test(raw)) return `<span class="hdr">${s}</span>`
  if (/^[A-Za-z].*:\s*$/.test(raw) && raw.length < 30) return `<span class="key">${s}</span>`
  if (/^\s+(labctl|kubectl|for |curl |Open )/.test(raw)) return `<span class="cmd2">${s}</span>`
  const m = raw.match(/^([A-Za-z][A-Za-z ]+:)(\s+)(.*)$/)
  if (m) return `<span class="key">${esc(m[1])}</span>${esc(m[2])}${esc(m[3])}`
  return s
}

function buildFrames(scenes) {
  const frames = []
  const done = []
  const push = (active) => frames.push(done.join('\n') + (active != null ? '\n' + active : ''))
  for (const sc of scenes) {
    if (sc.t === 'comment') { done.push(`<span class="cm">${esc(sc.text)}</span>`); push(); push(); push() }
    else if (sc.t === 'cmd') {
      for (let i = 1; i <= sc.text.length; i += CHAR_PER_FRAME)
        push(`${PROMPT}<span class="in">${esc(sc.text.slice(0, i))}</span><span class="cur">▋</span>`)
      done.push(`${PROMPT}<span class="in">${esc(sc.text)}</span>`); push()
    } else if (sc.t === 'out') { for (const ln of sc.lines) { done.push(colorLine(ln)); push() } }
    else if (sc.t === 'hold') { for (let i = 0; i < sc.f; i++) push(`${PROMPT}<span class="cur">▋</span>`) }
  }
  push(`${PROMPT}<span class="cur">▋</span>`)
  return frames
}

function pageHtml(cols, rows, frames) {
  const css = `
  :root{color-scheme:dark}*{margin:0;padding:0;box-sizing:border-box}
  body{background:#05070d;font-family:'DejaVu Sans Mono',Menlo,Consolas,monospace}
  .win{width:${cols}ch;margin:18px auto;background:#0a0f1c;border:1px solid #1c2740;
       border-radius:10px;overflow:hidden;box-shadow:0 18px 60px rgba(0,0,0,.6)}
  .bar{height:34px;background:#111a2e;display:flex;align-items:center;padding:0 14px;
       gap:8px;border-bottom:1px solid #1c2740}
  .dot{width:12px;height:12px;border-radius:50%}
  .r{background:#ff5f57}.y{background:#febc2e}.g{background:#28c840}
  .title{color:#5f6f8c;font-size:12px;margin-left:10px;letter-spacing:.3px}
  .body{padding:14px 16px;height:${rows}em;overflow:hidden;font-size:13.5px;line-height:1.5em}
  .body pre{white-space:pre;color:#c4d0e4;font:inherit}
  .u{color:#5eead4}.at{color:#5f6f8c}.h{color:#7dd3fc}.c{color:#5f6f8c}
  .p{color:#818cf8}.d{color:#5f6f8c}.in{color:#e8eefc}.cm{color:#5f7599;font-style:italic}
  .cur{color:#7dd3fc}.hdr{color:#7dd3fc;font-weight:600}.ok{color:#4ade80;font-weight:600}
  .err{color:#f87171}.key{color:#93a4c4}.cmd2{color:#a5b4fc}`
  return `<!doctype html><meta charset="utf-8"><style>${css}</style>
<div class="win"><div class="bar"><span class="dot r"></span><span class="dot y"></span>
<span class="dot g"></span><span class="title">labctl — SnowOps Labs</span></div>
<div class="body"><pre id="scr"></pre></div></div>
<script>const FR=${JSON.stringify(frames)};window.TOTAL=FR.length;
const scr=document.getElementById('scr'),body=document.querySelector('.body');
window.setFrame=i=>{scr.innerHTML=FR[Math.max(0,Math.min(i,FR.length-1))];body.scrollTop=body.scrollHeight};
window.setFrame(0);</script>`
}

// --- gif encode (with consecutive-identical-frame dedup) ------------------
function scaleTo(src, targetW) {
  if (src.w <= targetW) return src
  const s = targetW / src.w, tw = targetW, th = Math.round(src.h * s)
  const data = Buffer.alloc(tw * th * 4)
  for (let y = 0; y < th; y++) {
    const sy = Math.min(src.h - 1, Math.floor(y / s))
    for (let x = 0; x < tw; x++) {
      const sx = Math.min(src.w - 1, Math.floor(x / s))
      const si = (sy * src.w + sx) * 4, di = (y * tw + x) * 4
      data[di] = src.data[si]; data[di+1] = src.data[si+1]
      data[di+2] = src.data[si+2]; data[di+3] = src.data[si+3]
    }
  }
  return { w: tw, h: th, data }
}

function encodeGif(dir, outFile, width) {
  const delay = Math.round(1000 / FPS)
  const files = fs.readdirSync(dir).filter((f) => f.endsWith('.png')).sort()
  const kept = []
  let prev = null
  for (const f of files) {
    const png = PNG.sync.read(fs.readFileSync(path.join(dir, f)))
    const frame = scaleTo({ w: png.width, h: png.height, data: png.data }, width)
    let h = 0
    for (let b = 0; b < frame.data.length; b += 89) h = ((h * 33) ^ frame.data[b]) >>> 0
    if (h === prev && kept.length) { kept[kept.length - 1].delay += delay; continue }
    prev = h
    kept.push({ ...frame, delay })
  }
  const enc = GIFEncoder()
  for (const f of kept) {
    const palette = quantize(f.data, 96, { format: 'rgb444' })
    enc.writeFrame(applyPalette(f.data, palette, 'rgb444'), f.w, f.h, { palette, delay: f.delay })
  }
  enc.finish()
  fs.writeFileSync(outFile, enc.bytes())
  return { frames: kept.length, kb: Math.round(fs.statSync(outFile).size / 1024) }
}

// --- drive ----------------------------------------------------------------
const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {})
for (const [name, spec] of Object.entries(GIFS)) {
  const frames = buildFrames(spec.scenes)
  const htmlPath = path.join(tmp, `${name}.html`)
  fs.writeFileSync(htmlPath, pageHtml(spec.cols, spec.rows, frames))
  const framesDir = path.join(tmp, `frames-${name}`)
  fs.mkdirSync(framesDir, { recursive: true })
  const page = await browser.newPage({ deviceScaleFactor: 2 })
  await page.goto('file://' + htmlPath)
  const total = await page.evaluate(() => window.TOTAL)
  const win = page.locator('.win')
  for (let i = 0; i < total; i++) {
    await page.evaluate((n) => window.setFrame(n), i)
    await win.screenshot({ path: path.join(framesDir, `f${String(i).padStart(4, '0')}.png`) })
  }
  await page.close()
  const outFile = path.join(OUT, `${name}.gif`)
  const { frames: kf, kb } = encodeGif(framesDir, outFile, spec.width)
  console.log(`wrote docs/assets/${name}.gif  (${total} frames -> ${kf} kept, ${kb} KB)`)
}
await browser.close()
fs.rmSync(tmp, { recursive: true, force: true })
console.log('done')
