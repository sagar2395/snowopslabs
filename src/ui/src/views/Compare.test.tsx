// SPDX-License-Identifier: Apache-2.0
//
// Comparison view behaviour: the conditions that make a run comparable are
// shown with the numbers, a verdict never depends on colour alone, and a
// metric the cluster could not export is a dash rather than a zero.
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { axe, toHaveNoViolations } from 'jest-axe'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { Compare } from './Compare'
import type { Comparison, ComparisonMetric } from '../types'

expect.extend(toHaveNoViolations)

vi.mock('../api/client', () => ({ api: { getComparisons: vi.fn() } }))
import { api } from '../api/client'
const mockApi = api as unknown as { getComparisons: Mock }

function renderCompare() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Compare notify={vi.fn()} />
    </QueryClientProvider>,
  )
}

const metric = (key: string, label: string, unit: string, lowerBetter: boolean,
                scale = 1, digits = 1, neutral = false): ComparisonMetric =>
  ({ key, label, unit, lowerBetter, neutral, scale, digits })

const METRICS: ComparisonMetric[] = [
  metric('requests_per_second', 'throughput', 'req/s', false),
  metric('latency_p99', 'latency p99', 'ms', true, 1000, 1),
  metric('memory_working_set', 'memory', 'MiB', true, 1 / (1024 * 1024), 1),
  metric('peak_ready_replicas', 'peak ready replicas', '', false, 1, 0, true),
]

const comparison = (over: Partial<Comparison> = {}): Comparison => ({
  id: '20260909-140000-autoscaling-under-load',
  scenario: 'autoscaling-under-load',
  apps: ['go-api', 'java-api'],
  profile: 'steady', rps: 30, warmupSeconds: 60, windowSeconds: 180,
  startedAt: '2026-09-09T14:00:00Z',
  metrics: METRICS,
  measurements: [
    { app: 'go-api',   values: { requests_per_second: 30, latency_p99: 0.005, memory_working_set: 9 * 1024 * 1024, peak_ready_replicas: 1 } },
    { app: 'java-api', values: { requests_per_second: 30, latency_p99: 0.010, memory_working_set: 33 * 1024 * 1024, peak_ready_replicas: 1 } },
  ],
  ...over,
})

describe('Compare view', () => {
  beforeEach(() => vi.clearAllMocks())

  it('offers the command to run one when there are none', async () => {
    mockApi.getComparisons.mockResolvedValue([])
    renderCompare()
    expect(await screen.findByText(/no comparisons recorded yet/i)).toBeInTheDocument()
    expect(screen.getAllByText(/labctl compare run/).length).toBeGreaterThan(0)
  })

  // A comparison cannot be started here, so the view has to say what one is and
  // how to run it — otherwise it is a results table for a feature the reader has
  // no way to reach.
  it('explains what a comparison is and how to start one, with results or without', async () => {
    mockApi.getComparisons.mockResolvedValue([comparison()])
    renderCompare()
    expect(await screen.findByText(/how a comparison works/i)).toBeInTheDocument()
    expect(screen.getByText(/one app is under load at a time/i)).toBeInTheDocument()
    expect(screen.getByText('labctl compare list')).toBeInTheDocument()
    expect(screen.getAllByText(/labctl compare run autoscaling-under-load --apps/).length).toBeGreaterThan(0)
  })

  // The conditions are what make two numbers comparable. Rendering the numbers
  // without them would invite exactly the comparison the harness refuses to make.
  it('shows the conditions every app was held to', async () => {
    mockApi.getComparisons.mockResolvedValue([comparison()])
    renderCompare()
    const conditions = await screen.findByLabelText(/conditions every app was held to/i)
    expect(within(conditions).getByText(/steady at 30 req\/s/)).toBeInTheDocument()
    expect(within(conditions).getByText(/excluded/)).toBeInTheDocument()
    expect(within(conditions).getByText('3m')).toBeInTheDocument()
  })

  it('scales raw values into the unit the server named', async () => {
    mockApi.getComparisons.mockResolvedValue([comparison()])
    renderCompare()
    // 0.005s → 5.0 ms, and 9 MiB of bytes → 9.0 MiB.
    expect(await screen.findByText('5.0 ms')).toBeInTheDocument()
    expect(screen.getByText('9.0 MiB')).toBeInTheDocument()
  })

  // Colour alone would read "+100%" on latency as an improvement.
  it('states the verdict in words, not colour', async () => {
    mockApi.getComparisons.mockResolvedValue([comparison()])
    renderCompare()
    // java-api's p99 is double the baseline's, on a lower-is-better metric.
    expect(await screen.findByText(/100% worse/)).toBeInTheDocument()
    // …and its memory is far higher, also worse.
    expect(screen.getByText(/267% worse/)).toBeInTheDocument()
  })

  it('marks the baseline column', async () => {
    mockApi.getComparisons.mockResolvedValue([comparison()])
    renderCompare()
    const header = await screen.findByRole('columnheader', { name: /go-api/i })
    expect(within(header).getByText(/baseline/i)).toBeInTheDocument()
  })

  // A metric with no series means "not measurable here", which is not zero.
  it('shows a dash for a metric the cluster did not export', async () => {
    const c = comparison()
    c.measurements[1].values = { requests_per_second: 30 }
    mockApi.getComparisons.mockResolvedValue([c])
    renderCompare()
    expect(await screen.findAllByText('—')).not.toHaveLength(0)
  })

  // Replica count has no better direction, so no verdict is claimed.
  it('claims no verdict for a metric with no better direction', async () => {
    const c = comparison()
    c.measurements[1].values.peak_ready_replicas = 3
    mockApi.getComparisons.mockResolvedValue([c])
    renderCompare()
    const row = await screen.findByRole('row', { name: /peak ready replicas/i })
    expect(within(row).queryByText(/better|worse/)).toBeNull()
  })

  it('switches between recorded runs', async () => {
    const older = comparison({ id: 'older', scenario: 'cost-right-sizing', startedAt: '2026-09-08T10:00:00Z' })
    mockApi.getComparisons.mockResolvedValue([comparison(), older])
    renderCompare()
    // The newest run is shown first; the table's caption names what is on screen.
    expect(await screen.findByText(/autoscaling-under-load measured across/i)).toBeInTheDocument()

    await userEvent.click(await screen.findByRole('button', { name: /cost-right-sizing/i }))
    expect(await screen.findByText(/cost-right-sizing measured across/i)).toBeInTheDocument()
  })

  it('has no accessibility violations', async () => {
    mockApi.getComparisons.mockResolvedValue([comparison()])
    const { container } = renderCompare()
    await screen.findByLabelText(/conditions every app was held to/i)
    expect(await axe(container)).toHaveNoViolations()
  })
})
