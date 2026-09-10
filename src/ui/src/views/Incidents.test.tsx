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
    listApps: vi.fn(),
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
  listApps: Mock
}

const fault = (name: string, over: Partial<Fault> = {}): Fault => ({
  name, displayName: name, description: `desc ${name}`, verified: true,
  category: 'workload', severity: 'high',
  workload: { app: 'go-api', namespace: 'go-api', service: 'go-api.go-api.svc.cluster.local', port: '8080', metric: 'http_server_request_duration_seconds' },
  ...over,
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
  beforeEach(() => {
    vi.clearAllMocks()
    mockApi.listApps.mockResolvedValue([
      { name: 'go-api', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true },
      { name: 'java-api', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true },
    ])
  })

  it('lists the fault library and injects a fault', async () => {
    const user = userEvent.setup()
    mockApi.listIncidents.mockResolvedValue(twoFaults)
    mockApi.injectIncident.mockResolvedValue({ status: 'injected', silent: false })
    let captured: ConfirmRequest | null = null

    renderIncidents(r => { captured = r })
    expect(await screen.findByText('oom-kill')).toBeInTheDocument()
    expect(screen.getByText('network-blackhole')).toBeInTheDocument()

    // Injecting confirms first: which workload to break is part of the decision.
    const row = screen.getByText('oom-kill').closest('.scenario-row') as HTMLElement
    await user.click(within(row).getByRole('button', { name: /^inject$/i }))
    await waitFor(() => expect(captured).not.toBeNull())
    expect(mockApi.injectIncident).not.toHaveBeenCalled()
    captured!.onConfirm()
    await waitFor(() => expect(mockApi.injectIncident).toHaveBeenCalledWith('oom-kill', 'go-api'))
  })

  // A fault names its workload rather than an app (ADR-0014). Choosing which app
  // to break was reachable only from the CLI's --app flag.
  it('lets the user choose which application to break', async () => {
    const user = userEvent.setup()
    mockApi.listIncidents.mockResolvedValue(twoFaults)
    mockApi.injectIncident.mockResolvedValue({ status: 'injected', silent: false })
    let captured: ConfirmRequest | null = null

    renderIncidents(r => { captured = r })
    const row = (await screen.findByText('oom-kill')).closest('.scenario-row') as HTMLElement
    await user.click(within(row).getByRole('button', { name: /^inject$/i }))
    await waitFor(() => expect(captured).not.toBeNull())
    render(<div>{captured!.message as React.ReactNode}</div>)

    await user.selectOptions(await screen.findByLabelText(/application to break/i), 'java-api')
    captured!.onConfirm()
    await waitFor(() => expect(mockApi.injectIncident).toHaveBeenCalledWith('oom-kill', 'java-api'))
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

  // Only what the user must install. The app the fault resolved from the binding
  // is what gets broken, not a prerequisite — listing it said content that runs
  // against any conforming app ran against one.
  it('surfaces a fault\'s prerequisites so required tools are visible before injecting', async () => {
    mockApi.listIncidents.mockResolvedValue({
      faults: [fault('network-blackhole', {
        category: 'network',
        prerequisites: { platform: ['ingress'], apps: ['go-api'] },
        pinnedApps: [],
      })],
      active: null,
    })
    renderIncidents()
    const row = (await screen.findByText('network-blackhole')).closest('.scenario-row') as HTMLElement
    expect(within(row).getByText(/Requires:\s*ingress$/)).toBeInTheDocument()
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
