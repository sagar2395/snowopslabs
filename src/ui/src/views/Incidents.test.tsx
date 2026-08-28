// SPDX-License-Identifier: Apache-2.0
//
// Incidents view behaviour: browse the library, inject/resolve, and reveal hints
// progressively — plus an axe pass (W6-T10). The api boundary is mocked (see
// Traffic.test.tsx for why the client isn't exercised over MSW here).
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { axe, toHaveNoViolations } from 'jest-axe'
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { Incidents } from './Incidents'
import type { Fault, IncidentList } from '../types'
import type { ConfirmRequest } from '../components/ConfirmDialog'

expect.extend(toHaveNoViolations)

vi.mock('../api/client', () => ({
  api: {
    listIncidents: vi.fn(),
    getIncidentStatus: vi.fn(),
    getIncidentHistory: vi.fn(),
    injectIncident: vi.fn(),
    injectRandomIncident: vi.fn(),
    resolveIncident: vi.fn(),
    nextIncidentHint: vi.fn(),
  },
}))
import { api } from '../api/client'
const mockApi = api as unknown as {
  listIncidents: Mock
  getIncidentStatus: Mock
  injectIncident: Mock
  injectRandomIncident: Mock
  resolveIncident: Mock
  nextIncidentHint: Mock
}

const fault = (name: string, over: Partial<Fault> = {}): Fault => ({
  name, displayName: name, description: `desc ${name}`, verified: true,
  category: 'workload', severity: 'high', ...over,
})

// requestConfirm that immediately fires onConfirm, so a confirmed action runs.
const autoConfirm = (req: ConfirmRequest) => { void req.onConfirm() }

function renderIncidents(confirm: (r: ConfirmRequest) => void = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const notify = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <Incidents notify={notify} requestConfirm={confirm} />
    </QueryClientProvider>,
  )
  return { notify }
}

const twoFaults: IncidentList = {
  faults: [fault('oom-kill'), fault('network-blackhole', { category: 'network', severity: 'medium' })],
  active: null,
}

describe('Incidents view', () => {
  beforeEach(() => vi.clearAllMocks())

  it('lists the fault library and injects a fault', async () => {
    const user = userEvent.setup()
    mockApi.listIncidents.mockResolvedValue(twoFaults)
    mockApi.injectIncident.mockResolvedValue({ status: 'injected', silent: false })

    renderIncidents()
    expect(await screen.findByText('oom-kill')).toBeInTheDocument()
    expect(screen.getByText('network-blackhole')).toBeInTheDocument()

    const row = screen.getByText('oom-kill').closest('.scenario-row') as HTMLElement
    await user.click(within(row).getByRole('button', { name: /^inject$/i }))
    await waitFor(() => expect(mockApi.injectIncident).toHaveBeenCalledWith('oom-kill'))
  })

  it('shows the active console, reveals hints progressively, and disables further injects', async () => {
    const user = userEvent.setup()
    mockApi.listIncidents.mockResolvedValue({
      ...twoFaults,
      active: { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: false, hintsRevealed: 0 },
    })
    mockApi.nextIncidentHint
      .mockResolvedValueOnce({ index: 1, total: 2, text: 'Check the memory limits' })
      .mockResolvedValueOnce({ index: 2, total: 2, text: 'Raise the deployment resources' })

    renderIncidents()
    expect(await screen.findByText(/Active: oom-kill/)).toBeInTheDocument()
    // Injecting another fault is blocked while one is active.
    const row = screen.getByText('network-blackhole').closest('.scenario-row') as HTMLElement
    expect(within(row).getByRole('button', { name: /inject/i })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: /reveal a hint/i }))
    expect(await screen.findByText('Check the memory limits')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /reveal next hint/i }))
    expect(await screen.findByText('Raise the deployment resources')).toBeInTheDocument()
    // Both hints shown, and the button reports exhaustion.
    expect(await screen.findByRole('button', { name: /no more hints/i })).toBeDisabled()
  })

  it('resolves the active incident after confirmation', async () => {
    const user = userEvent.setup()
    mockApi.listIncidents.mockResolvedValue({
      ...twoFaults,
      active: { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: false, hintsRevealed: 0 },
    })
    mockApi.resolveIncident.mockResolvedValue({ status: 'resolved', fault: 'oom-kill' })

    renderIncidents(autoConfirm)
    await screen.findByText(/Active: oom-kill/)
    await user.click(screen.getByRole('button', { name: /^resolve$/i }))
    await waitFor(() => expect(mockApi.resolveIncident).toHaveBeenCalled())
  })

  it('hides the fault identity in silent mode until resolved', async () => {
    mockApi.listIncidents.mockResolvedValue({
      ...twoFaults,
      active: { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: true, hintsRevealed: 0 },
    })
    renderIncidents()
    expect(await screen.findByText(/hidden \(silent mode\)/i)).toBeInTheDocument()
    // The fault name is not revealed in the active console heading.
    expect(screen.queryByText(/Active: oom-kill/)).not.toBeInTheDocument()
  })

  it('surfaces a fault\'s prerequisites so required tools are visible before injecting', async () => {
    mockApi.listIncidents.mockResolvedValue({
      faults: [fault('network-blackhole', { category: 'network', prerequisites: { platform: ['ingress'], apps: ['go-api'] } })],
      active: null,
    })
    renderIncidents()
    const row = (await screen.findByText('network-blackhole')).closest('.scenario-row') as HTMLElement
    expect(within(row).getByText(/Requires:\s*ingress\s*·\s*go-api/)).toBeInTheDocument()
  })

  it('has no accessibility violations', async () => {
    mockApi.listIncidents.mockResolvedValue(twoFaults)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { container } = render(
      <QueryClientProvider client={client}>
        <Incidents notify={vi.fn()} requestConfirm={vi.fn()} />
      </QueryClientProvider>,
    )
    await screen.findByText('oom-kill')
    expect(await axe(container)).toHaveNoViolations()
  })
})
