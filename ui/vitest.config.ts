import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Component-layer test config (test layer 4a — see docs/TESTING.md).
//
// API calls are mocked at the network layer with MSW, never by stubbing
// modules, so tests exercise the real fetch path and catch serialisation
// mistakes that module stubbing would hide.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    // Playwright specs live in e2e/ and are driven by playwright.config.ts.
    exclude: ['node_modules/**', 'dist/**', 'e2e/**'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/main.tsx',
        'src/test/**',
        'src/types/**', // type-only declarations carry no statements
        // Coverage ratchet — the UI analogue of .coverage-exceptions. These
        // views, hooks and pre-rebuild components are scheduled for replacement
        // in the W6/W7 UI rebuild; testing them today only to delete them is
        // waste. Delete an entry when its module gets real tests, never add one
        // to dodge the gate. Everything NOT listed here must hold the 80% bar.
        'src/App.tsx',                    // W6 — shell rebuilt with the new router
        'src/views/**',                   // W6/W7 — every view rebuilt
        'src/hooks/**',                   // W6 — useJobRunner/useWebSocket reworked
        'src/lib/jobs.ts',                // W6 — job polling folded into hooks
        'src/components/ConfirmDialog.tsx', // W6 — rebuilt with the design system
        'src/components/ErrorState.tsx',    // W6
        'src/components/LogPanel.tsx',      // W6 — moves onto the run-engine stream
        'src/components/Login.tsx',         // W6
        'src/components/Notification.tsx',  // W6
      ],
      // Matches the Go bar in docs/TESTING.md. The exclude list above is the
      // ratchet that lets the pre-rebuild UI meet it honestly; as W6/W7 lands,
      // entries come off and the gate widens.
      thresholds: {
        statements: 80,
        branches: 70,
        functions: 80,
        lines: 80,
      },
    },
  },
})
