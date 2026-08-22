import { defineConfig, devices } from '@playwright/test'

// Journey-layer test config (test layer 4b — see docs/TESTING.md).
//
// Playwright drives a real browser against the built SPA. Backend responses are
// stubbed per-test with page.route(), so journeys exercise real routing, real
// rendering and real streaming without needing a cluster.
//
// From W6 the target becomes the labctl binary itself running with a fake
// toolchain; until the rebuild lands, `vite preview` serves the SPA.
//
// Browser resolution: normally Playwright's own managed download. Environments
// that ship a browser (CI images, dev containers, air-gapped builds) can point
// at it with PLAYWRIGHT_CHROMIUM_EXECUTABLE instead of downloading a second
// copy. Unset, behaviour is stock Playwright.
const PORT = Number(process.env.PLAYWRIGHT_PORT ?? 4173)
const chromiumPath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE

export default defineConfig({
  testDir: './e2e',
  // A journey that hangs is a failure, not something to wait out.
  timeout: 30_000,
  expect: { timeout: 5_000 },
  fullyParallel: true,
  // A test that only passes on a retry is flaky; fail the build instead of
  // hiding it. CI forbids .only sneaking in.
  retries: 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        ...(chromiumPath
          ? {
              launchOptions: {
                executablePath: chromiumPath,
                // Containers commonly run as root, where the sandbox refuses
                // to start. Only relaxed for a supplied browser, never for a
                // developer's stock install.
                args: ['--no-sandbox', '--disable-dev-shm-usage'],
              },
            }
          : {}),
      },
    },
  ],
  webServer: {
    // Bind the preview to 127.0.0.1 explicitly. Left to its default host,
    // `vite preview` binds `localhost`, which on macOS resolves to IPv6 `::1`
    // only — while `url`/`baseURL` below reach for IPv4 `127.0.0.1`, so the
    // health check never connects and the run times out. Pinning the host to
    // 127.0.0.1 keeps server and client on the same address on macOS and Linux
    // alike (golden rule 1).
    command: `npm run build && npx vite preview --port ${PORT} --strictPort --host 127.0.0.1`,
    url: `http://127.0.0.1:${PORT}`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
})
