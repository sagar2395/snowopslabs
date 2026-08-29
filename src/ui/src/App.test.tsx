// SPDX-License-Identifier: Apache-2.0
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, it, expect } from 'vitest'
import App from './App'
import { server, API } from './test/server'

// Render the app at a specific URL. App declares <Routes> (not a Router) and its
// views read from the query cache, so the test supplies both the MemoryRouter
// (standing in for main.tsx's BrowserRouter) and a fresh QueryClient — fresh so
// no cached data leaks between tests. Retries off so an error surfaces at once.
function renderAt(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('routing', () => {
  it('deep-links directly to a section from its URL', async () => {
    renderAt('/scenarios')
    // The Scenarios nav link is marked active because the URL selected it —
    // proving the view is chosen by the route, not by in-memory tab state.
    const link = await screen.findByRole('link', { name: 'Scenarios' })
    await waitFor(() => expect(link).toHaveClass('active'))
    expect(link).toHaveAttribute('aria-current', 'page')
  })

  it('redirects the index path to the dashboard', async () => {
    // Dashboard mounts here and fetches /dashboards; mock it so the strict
    // unhandled-request policy is satisfied.
    server.use(http.get(`${API}/dashboards`, () => HttpResponse.json([])))
    renderAt('/')
    // The redirect resolves asynchronously; wait for the active state rather
    // than asserting on the (always-present) link the instant it renders.
    const link = await screen.findByRole('link', { name: 'Dashboard' })
    await waitFor(() => expect(link).toHaveClass('active'))
  })

  it('falls back to the dashboard for an unknown path', async () => {
    server.use(http.get(`${API}/dashboards`, () => HttpResponse.json([])))
    renderAt('/does-not-exist')
    const link = await screen.findByRole('link', { name: 'Dashboard' })
    await waitFor(() => expect(link).toHaveClass('active'))
  })
})
