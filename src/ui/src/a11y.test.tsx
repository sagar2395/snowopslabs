// SPDX-License-Identifier: Apache-2.0
//
// Automated accessibility checks (W6-T10). axe runs against the rendered DOM and
// fails on any serious/critical ARIA, role, name, or structure violation. Colour
// contrast is not checkable in jsdom (no layout); that is handled by the tuned
// design tokens and the Playwright+axe e2e complement.

import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { axe, toHaveNoViolations } from 'jest-axe'
import { describe, it, expect } from 'vitest'
import App from './App'
import { server, API } from './test/server'
import { ThemeToggle } from './components/ThemeToggle'
import { CommandPalette } from './components/CommandPalette'
import { ErrorState } from './components/ErrorState'

expect.extend(toHaveNoViolations)

describe('accessibility (axe)', () => {
  it('ThemeToggle has no violations', async () => {
    const { container } = render(<ThemeToggle />)
    expect(await axe(container)).toHaveNoViolations()
  })

  it('CommandPalette has no violations when open', async () => {
    const { container } = render(
      <CommandPalette
        open
        onClose={() => {}}
        commands={[
          { id: 'a', label: 'Go to Dashboard', hint: '/dashboard', run: () => {} },
          { id: 'b', label: 'Go to Results', hint: '/results', run: () => {} },
        ]}
      />,
    )
    expect(await axe(container)).toHaveNoViolations()
  })

  it('ErrorState has no violations', async () => {
    const { container } = render(
      <ErrorState title="Failed to load" message="the server is unreachable" onRetry={() => {}} retrying={false} />,
    )
    expect(await axe(container)).toHaveNoViolations()
  })

  it('the app shell (skip link, header, nav, main) has no violations', async () => {
    server.use(http.get(`${API}/dashboards`, () => HttpResponse.json([])))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { container } = render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={['/dashboard']}>
          <App />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    // Wait for the shell to mount past the auth/loading gate.
    await waitFor(() => expect(screen.getByRole('navigation', { name: /sections/i })).toBeInTheDocument())
    expect(await axe(container)).toHaveNoViolations()
  })
})
