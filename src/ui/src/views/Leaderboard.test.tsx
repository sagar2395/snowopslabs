// SPDX-License-Identifier: Apache-2.0
//
// Leaderboard view against the contract server (test/contract.ts).
import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { serveContract } from '../test/contract'
import { Leaderboard, periodStart } from './Leaderboard'

afterEach(() => {
  vi.unstubAllGlobals()
})

function renderBoard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <Leaderboard notify={vi.fn()} />
    </QueryClientProvider>,
  )
}

const entry = (user: string, incidentsResolved: number, avgMttrSeconds: number) => ({
  user, totalScore: 0, challengesCompleted: 0, incidentsResolved, modulesCompleted: 0, hintsUsed: 1, avgMttrSeconds, runs: 1,
})

describe('Leaderboard view', () => {
  it('tells a new lab how to get onto the board', async () => {
    const server = serveContract()
    server.override('GET', '/leaderboard', 200, [])
    renderBoard()
    expect(await screen.findByText('No results yet - run a challenge or incident.')).toBeInTheDocument()
    expect(screen.getByText('Leaderboard (0)')).toBeInTheDocument()
  })

  it('ranks every run by default, and only recent runs once a period is picked', async () => {
    const server = serveContract()
    server.overrideEach('GET', '/leaderboard', 200, [
      [entry('old-hand', 300, 40), entry('alice', 1, 53)],
      [entry('alice', 1, 53)],
    ])
    renderBoard()
    expect(await screen.findByText('old-hand')).toBeInTheDocument()
    expect(server.calls[server.calls.length - 1]?.query.get('since')).toBeNull()

    fireEvent.change(screen.getByLabelText('Runs to rank'), { target: { value: 'today' } })
    expect(await screen.findByText('Leaderboard (1)')).toBeInTheDocument()
    expect(screen.queryByText('old-hand')).not.toBeInTheDocument()
    expect(screen.getByText('alice')).toBeInTheDocument()
    expect(screen.getByText('0:53')).toBeInTheDocument()
    const since = server.calls[server.calls.length - 1]?.query.get('since')
    expect(since).toBe(periodStart('today'))
  })

  it('says a period has no runs instead of telling a busy lab it has none', async () => {
    const server = serveContract()
    server.override('GET', '/leaderboard', 200, [])
    renderBoard()
    fireEvent.change(await screen.findByLabelText('Runs to rank'), { target: { value: '4h' } })
    expect(await screen.findByText('No runs in this period (last 4 hours).')).toBeInTheDocument()
  })

  it('shows the server error with a retry', async () => {
    const server = serveContract()
    server.override('GET', '/leaderboard', 400, {
      type: 'https://snowopslabs.dev/problems/invalid_input', title: 'Bad Request', status: 400,
      detail: 'since must be an RFC 3339 time, such as 2026-11-07T09:00:00Z',
    })
    renderBoard()
    expect(await screen.findByText('Failed to load leaderboard')).toBeInTheDocument()
    expect(screen.getByText(/since must be an RFC 3339 time/)).toBeInTheDocument()
  })
})

describe('periodStart', () => {
  const now = new Date(2026, 10, 7, 14, 30, 0)
  it('is undefined for all time', () => {
    expect(periodStart('all', now)).toBeUndefined()
  })
  it('is local midnight for today', () => {
    expect(periodStart('today', now)).toBe(new Date(2026, 10, 7, 0, 0, 0).toISOString())
  })
  it('is four hours back for the last 4 hours', () => {
    expect(periodStart('4h', now)).toBe(new Date(2026, 10, 7, 10, 30, 0).toISOString())
  })
})
