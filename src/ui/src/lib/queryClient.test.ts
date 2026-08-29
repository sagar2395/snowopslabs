// SPDX-License-Identifier: Apache-2.0
import { describe, it, expect } from 'vitest'
import { queryClient, qk } from './queryClient'

describe('queryClient', () => {
  it('configures the shared defaults views rely on', () => {
    const defaults = queryClient.getDefaultOptions().queries
    expect(defaults?.staleTime).toBe(15_000)
    expect(defaults?.retry).toBe(1)
    expect(defaults?.refetchOnWindowFocus).toBe(false)
  })
})

describe('qk query-key registry', () => {
  it('exposes stable keys for cache reads and invalidation', () => {
    expect(qk.status).toEqual(['status'])
    expect(qk.incidentHistory).toEqual(['incidents', 'history'])
    expect(qk.learnPaths).toEqual(['learn', 'paths'])
  })

  it('scopes a single run by id', () => {
    // qk.run is the one computed key — a per-run scope nested under 'runs' so
    // invalidating the list and a single run stay independent.
    expect(qk.run('abc123')).toEqual(['runs', 'abc123'])
    expect(qk.run('abc123')).not.toEqual(qk.runs)
  })
})
