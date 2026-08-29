// SPDX-License-Identifier: Apache-2.0
import { render, screen, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect } from 'vitest'
import { useApiQuery } from './useApiQuery'

// A probe component whose query function counts its calls, so we can assert how
// many times the data was actually fetched.
function makeProbe() {
  let calls = 0
  const queryFn = async () => {
    calls += 1
    return calls
  }
  function Probe() {
    const { data, loading } = useApiQuery(['probe'], queryFn)
    return <div>{loading ? 'loading' : `count:${data}`}</div>
  }
  return { Probe, queryFn }
}

describe('useApiQuery', () => {
  it('fetches once on mount and exposes the data', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { Probe } = makeProbe()
    render(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>,
    )
    // First render shows the loading state, then the fetched value.
    expect(await screen.findByText('count:1')).toBeInTheDocument()
  })

  it('refetches when the query is invalidated (run-completion signal)', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { Probe } = makeProbe()
    render(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>,
    )
    expect(await screen.findByText('count:1')).toBeInTheDocument()

    // This is exactly what the app does on action_end: invalidate, which reruns
    // every mounted query.
    await act(async () => {
      await client.invalidateQueries()
    })
    expect(await screen.findByText('count:2')).toBeInTheDocument()
  })
})
