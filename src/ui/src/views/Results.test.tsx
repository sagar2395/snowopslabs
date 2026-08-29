// SPDX-License-Identifier: Apache-2.0
//
// Results view behaviour (W7-T04): summary stats, the MTTR/MTTD + score trend
// charts, and the per-check breakdown in the history table. The api boundary is
// mocked (see Traffic.test.tsx for why the client isn't exercised over MSW here).
import { render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { axe, toHaveNoViolations } from 'jest-axe'
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { Results } from './Results'
import type { ResultRecord } from '../types'

expect.extend(toHaveNoViolations)

vi.mock('../api/client', () => ({
  api: { getResults: vi.fn(), getResultsByKind: vi.fn() },
}))
import { api } from '../api/client'
const mockApi = api as unknown as { getResults: Mock }

function renderResults() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <Results notify={vi.fn()} />
    </QueryClientProvider>,
  )
}

const incident = (name: string, mttr: number, mttd: number, at: string): ResultRecord => ({
  kind: 'incident', name, startedAt: at, endedAt: at, elapsedSeconds: mttr,
  score: -1, outcome: 'resolved', meta: { detectSeconds: mttd },
})
const challenge = (name: string, score: number, at: string, passed = 3, total = 4): ResultRecord => ({
  kind: 'challenge', name, startedAt: at, endedAt: at, elapsedSeconds: 120,
  score, outcome: 'passed', hintsUsed: 1, meta: { checksPassed: passed, checksTotal: total },
})

describe('Results view', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows a designed empty state with no runs', async () => {
    mockApi.getResults.mockResolvedValue([])
    renderResults()
    expect(await screen.findByText(/no runs recorded yet/i)).toBeInTheDocument()
    // No charts when there's nothing to plot.
    expect(screen.queryByRole('img', { name: /trend/i })).not.toBeInTheDocument()
  })

  it('renders MTTR/MTTD and score trend charts and avg stats', async () => {
    mockApi.getResults.mockResolvedValue([
      incident('oom-kill', 90, 20, '2026-08-20T10:00:00Z'),
      incident('oom-kill', 45, 10, '2026-08-21T10:00:00Z'),
      challenge('deploy-fix', 80, '2026-08-22T10:00:00Z'),
    ])
    renderResults()

    expect(await screen.findByRole('img', { name: /MTTR and MTTD trend/i })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: /challenge score trend/i })).toBeInTheDocument()
    // Avg MTTR tile present (average of 90 and 45 = 67s -> "1m 7s").
    expect(screen.getByText('Avg MTTR')).toBeInTheDocument()
    expect(screen.getByText('Avg MTTD')).toBeInTheDocument()
  })

  it('shows the challenge check breakdown in the history table', async () => {
    mockApi.getResults.mockResolvedValue([challenge('deploy-fix', 80, '2026-08-22T10:00:00Z', 3, 4)])
    renderResults()
    const table = await screen.findByRole('table')
    expect(within(table).getByText('3/4 checks')).toBeInTheDocument()
    expect(within(table).getByText(/1 hint/)).toBeInTheDocument()
  })

  it('has no accessibility violations with charts and a history table', async () => {
    mockApi.getResults.mockResolvedValue([
      incident('oom-kill', 90, 20, '2026-08-20T10:00:00Z'),
      challenge('deploy-fix', 80, '2026-08-22T10:00:00Z'),
    ])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { container } = render(
      <QueryClientProvider client={client}>
        <Results notify={vi.fn()} />
      </QueryClientProvider>,
    )
    await screen.findByRole('table')
    expect(await axe(container)).toHaveNoViolations()
  })

  it('does not render the incident chart when only challenges exist', async () => {
    mockApi.getResults.mockResolvedValue([challenge('deploy-fix', 70, '2026-08-22T10:00:00Z')])
    renderResults()
    await waitFor(() => expect(mockApi.getResults).toHaveBeenCalled())
    expect(await screen.findByRole('img', { name: /challenge score trend/i })).toBeInTheDocument()
    expect(screen.queryByRole('img', { name: /MTTR and MTTD/i })).not.toBeInTheDocument()
  })
})
