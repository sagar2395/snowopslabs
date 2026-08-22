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
      ],
      // Matches the Go bar in docs/TESTING.md. Raised to a real gate as the
      // W6/W7 rebuild replaces these views.
      thresholds: {
        statements: 80,
        branches: 70,
        functions: 80,
        lines: 80,
      },
    },
  },
})
