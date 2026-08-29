# SnowOps Labs — brand assets

The logo, icon mark, favicon, and social image. All are generated from a single
source of truth — `docs/assets/gen/brand.mjs` — so they stay consistent with the
web UI's design tokens (`src/ui/src/index.css`). Edit the generator, never the
individual SVGs by hand.

## The mark

A six-fold snow crystal on a rounded tile with the product's signature
sky→indigo gradient. The same glyph is used by the web UI header
(`Icon name="snowflake"`), the favicon, and the logo lockups, so the brand reads
as one system everywhere.

## Files

| File | Use |
|---|---|
| `mark.svg` / `mark.png` | Square icon mark (avatars, app tiles, docs) |
| `logo-dark.svg` / `logo-dark.png` | Horizontal lockup for **dark** backgrounds (light wordmark) |
| `logo-light.svg` / `logo-light.png` | Horizontal lockup for **light** backgrounds (dark wordmark) |
| `social-preview.svg` / `social-preview.png` | 1280×640 GitHub social preview |
| `../../../src/ui/public/favicon.svg` | Browser-tab favicon (served by `labctl ui`) |
| `../../../src/ui/public/favicon-32.png` | PNG favicon fallback |
| `../../../src/ui/public/apple-touch-icon.png` | 180×180 iOS home-screen icon |

The README picks the light/dark lockup automatically with a `<picture>` element.

## Palette

| Token | Hex | Role |
|---|---|---|
| `--accent` | `#38bdf8` | Primary (sky) — "SnowOps", links, focus |
| `--accent-2` | `#818cf8` | Secondary (indigo) — gradient end |
| `--text-strong` | `#f8fafc` | Wordmark & crystal on dark |
| `--on-accent` / ink | `#0f172a` | Wordmark on light |
| background | `#05070d`→`#0f1729` | App / banner ground |

The wordmark is set in a bold monospace (`DejaVu Sans Mono`), matching the UI's
`--font-display` "developer console" feel.

## Regenerating

```bash
node docs/assets/gen/brand.mjs      # rewrites every SVG and PNG above
```

Reuses the Playwright Chromium `src/ui` already depends on. Set
`CHROME=/path/to/chromium` if it can't find a browser.

## Social preview

GitHub's repo social image is a **repository setting**, not a file it reads
automatically. A maintainer uploads `social-preview.png` once under
**Settings → General → Social preview**.
