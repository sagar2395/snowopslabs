// SPDX-License-Identifier: Apache-2.0
//
// View-behaviour test. The api boundary is mocked (not MSW) because the
// component layer runs in jsdom, where the client's AbortController fetch is
// incompatible with undici — see Traffic.test.tsx for the full rationale.
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { Runs } from './Runs'
import type { RunSummary, RunDetail, RunLogs } from '../types'

vi.mock('../api/client', () => ({
  api: {
    listRuns: vi.fn(),
    getRun: vi.fn(),
    getRunLogs: vi.fn(),
    cancelRun: vi.fn(),
  },
}))

import { api } from '../api/client'
const mockApi = api as unknown as {
  listRuns: Mock
  getRun: Mock
  getRunLogs: Mock
  cancelRun: Mock
}

function renderRuns() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const notify = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <Runs notify={notify} />
    </QueryClientProvider>,
  )
  return { notify }
}

const upRun: RunSummary = {
  id: 'run_1', kind: 'lab.up', target: 'k3d', status: 'succeeded',
  queuedAt: new Date().toISOString(), startedAt: new Date().toISOString(), durationMs: 4200,
}
const runningRun: RunSummary = {
  id: 'run_2', kind: 'lab.down', target: 'k3d', status: 'running',
  queuedAt: new Date().toISOString(), startedAt: new Date().toISOString(),
}

function detailFrom(r: RunSummary): RunDetail {
  return { ...r, steps: [] }
}
function noLogs(status: RunSummary['status']): RunLogs {
  return { runId: 'x', status, lines: [], nextAfter: 0, done: status !== 'running' }
}

describe('Runs view', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockApi.getRun.mockImplementation((id: string) =>
      Promise.resolve(detailFrom(id === 'run_2' ? runningRun : upRun)))
    mockApi.getRunLogs.mockResolvedValue(noLogs('succeeded'))
  })

  it('lists recorded runs with their status', async () => {
    mockApi.listRuns.mockResolvedValue([upRun, runningRun])
    renderRuns()
    expect(await screen.findByText('lab.up · k3d')).toBeInTheDocument()
    // The list also renders the status badge.
    expect(screen.getAllByText('succeeded').length).toBeGreaterThan(0)
  })

  it('shows an empty state when there are no runs', async () => {
    mockApi.listRuns.mockResolvedValue([])
    renderRuns()
    expect(await screen.findByText(/No runs recorded yet/)).toBeInTheDocument()
  })

  it('auto-selects the newest run and streams its output', async () => {
    mockApi.listRuns.mockResolvedValue([upRun])
    mockApi.getRunLogs.mockResolvedValue({
      runId: 'run_1', status: 'succeeded', done: true, nextAfter: 2,
      lines: [
        { seq: 1, at: new Date().toISOString(), stream: 'stdout', text: 'creating cluster' },
        { seq: 2, at: new Date().toISOString(), stream: 'stdout', text: 'cluster ready' },
      ],
    })
    renderRuns()
    expect(await screen.findByText(/cluster ready/)).toBeInTheDocument()
    await waitFor(() => expect(mockApi.getRunLogs).toHaveBeenCalledWith('run_1', 0))
  })

  it('offers a cancel control only for a live run and calls the API', async () => {
    mockApi.listRuns.mockResolvedValue([runningRun])
    mockApi.getRunLogs.mockResolvedValue(noLogs('running'))
    mockApi.cancelRun.mockResolvedValue({ status: 'cancelled', runId: 'run_2' })

    const user = userEvent.setup()
    const { notify } = renderRuns()

    const cancelBtn = await screen.findByRole('button', { name: /^cancel$/i })
    await user.click(cancelBtn)
    await waitFor(() => expect(mockApi.cancelRun).toHaveBeenCalledWith('run_2'))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('info', expect.stringMatching(/Cancellation requested/), expect.anything()))
  })

  it('does not offer cancel for a finished run', async () => {
    mockApi.listRuns.mockResolvedValue([upRun])
    renderRuns()
    // Wait for the detail panel to render (run id metadata shows).
    const detail = await screen.findByText('Run ID')
    expect(within(detail.closest('.card') as HTMLElement).queryByRole('button', { name: /^cancel$/i })).toBeNull()
  })

  it('shows a designed error state when the list fails to load', async () => {
    mockApi.listRuns.mockRejectedValue(new Error('boom'))
    renderRuns()
    expect(await screen.findByText(/Failed to load runs/)).toBeInTheDocument()
  })
})
