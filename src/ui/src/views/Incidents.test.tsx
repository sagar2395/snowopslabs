// SPDX-License-Identifier: Apache-2.0
//
// Incidents view behaviour: browse the library, inject/resolve, and reveal hints
// progressively - plus an axe pass. Requests go through the real client to the
// contract server (test/contract.ts).
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { axe, toHaveNoViolations } from 'jest-axe'
import { afterEach, describe, it, expect, vi } from 'vitest'
import { serveContract, type ContractServer } from '../test/contract'
import { api, RequestTimeoutError } from '../api/client'
import { Incidents } from './Incidents'
import type { Fault, IncidentList } from '../types'
import type { ConfirmRequest } from '../components/ConfirmDialog'

expect.extend(toHaveNoViolations)

afterEach(() => {
  vi.unstubAllGlobals()
})

const APPS = [
  { name: 'go-api', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true },
  { name: 'java-api', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true },
]

/** A contract server answering GET /incidents with list and GET /apps with two apps. */
function serveIncidents(list: IncidentList): ContractServer {
  const server = serveContract()
  server.override('GET', '/incidents', 200, list)
  server.override('GET', '/apps', 200, APPS)
  return server
}

/** The query of the one inject request the view sent for fault. */
function injectQuery(server: ContractServer, fault: string) {
  return server.calls.find(c => c.method === 'POST' && c.path === `/incidents/${fault}/inject`)?.query
}

const fault = (name: string, over: Partial<Fault> = {}): Fault => ({
  name, displayName: name, description: `desc ${name}`, verified: true,
  category: 'workload', severity: 'high',
  workload: { app: 'go-api', namespace: 'go-api', service: 'go-api.go-api.svc.cluster.local', port: '8080', metric: 'http_server_request_duration_seconds' },
  ...over,
})

// requestConfirm that immediately fires onConfirm, so a confirmed action runs.
const autoConfirm = (req: ConfirmRequest) => { void req.onConfirm() }

function renderIncidents(confirm: (r: ConfirmRequest) => void = vi.fn(), canOperate?: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const notify = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <Incidents notify={notify} requestConfirm={confirm} canOperate={canOperate} />
    </QueryClientProvider>,
  )
  return { notify }
}

const twoFaults: IncidentList = {
  faults: [fault('oom-kill'), fault('network-blackhole', { category: 'network', severity: 'medium' })],
  active: null,
}

describe('Incidents view', () => {
  it('lists the faults the server reports', async () => {
    const server = serveContract()
    renderIncidents()
    expect(await screen.findByText('Demo fault')).toBeInTheDocument()
    expect(screen.getByText(/No active incident/)).toBeInTheDocument()
    expect(server.unanswered).toEqual([])
  })

  it('lists the fault library and injects a fault', async () => {
    const user = userEvent.setup()
    const server = serveIncidents(twoFaults)
    let captured: ConfirmRequest | null = null

    renderIncidents(r => { captured = r })
    expect(await screen.findByText('oom-kill')).toBeInTheDocument()
    expect(screen.getByText('network-blackhole')).toBeInTheDocument()

    // Injecting confirms first: which workload to break is part of the decision.
    const row = screen.getByText('oom-kill').closest('.scenario-row') as HTMLElement
    await user.click(within(row).getByRole('button', { name: /^inject$/i }))
    await waitFor(() => expect(captured).not.toBeNull())
    expect(injectQuery(server, 'oom-kill')).toBeUndefined()
    captured!.onConfirm()
    await waitFor(() => expect(injectQuery(server, 'oom-kill')?.get('app')).toBe('go-api'))
  })

  // A fault names its workload rather than an app (ADR-0014). Choosing which app
  // to break was reachable only from the CLI's --app flag.
  it('lets the user choose which application to break', async () => {
    const user = userEvent.setup()
    const server = serveIncidents(twoFaults)
    let captured: ConfirmRequest | null = null

    renderIncidents(r => { captured = r })
    const row = (await screen.findByText('oom-kill')).closest('.scenario-row') as HTMLElement
    await user.click(within(row).getByRole('button', { name: /^inject$/i }))
    await waitFor(() => expect(captured).not.toBeNull())
    render(<div>{captured!.message as React.ReactNode}</div>)

    await user.selectOptions(await screen.findByLabelText(/application to break/i), 'java-api')
    captured!.onConfirm()
    await waitFor(() => expect(injectQuery(server, 'oom-kill')?.get('app')).toBe('java-api'))
  })

  it('shows the active console, reveals hints progressively, and disables further injects', async () => {
    const user = userEvent.setup()
    const server = serveIncidents({
      ...twoFaults,
      active: { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: false, hintsRevealed: 0 },
    })
    server.overrideEach('POST', '/incidents/hint', 200, [
      { index: 1, total: 2, text: 'Check the memory limits' },
      { index: 2, total: 2, text: 'Raise the deployment resources' },
    ])

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

  it.each([
    {
      state: 'passes',
      status: { resolved: true, check: { name: 'rollout-healthy', pass: true } },
      banner: /Detection check rollout-healthy passes - resolved\./,
      toast: ['success', 'Detection check passes', 'The fault is resolved - nice work.'],
    },
    {
      state: 'still fails',
      status: { resolved: false, check: { name: 'rollout-healthy', pass: false, explanation: '0 of 1 replicas ready.' } },
      banner: /Not resolved yet - rollout-healthy fails: 0 of 1 replicas ready\./,
      toast: ['info', 'Still broken', '0 of 1 replicas ready.'],
    },
  ])('says whether the detection check $state when the learner checks', async ({ status, banner, toast }) => {
    const user = userEvent.setup()
    const active = { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: false, hintsRevealed: 0 }
    const server = serveIncidents({ ...twoFaults, active })
    server.override('GET', '/incidents/status', 200, { active, fault: twoFaults.faults[0], ...status })

    const { notify } = renderIncidents()
    await screen.findByText(/Active: oom-kill/)
    await user.click(screen.getByRole('button', { name: /check if resolved/i }))
    const shown = await screen.findByRole('status')
    expect(shown.textContent).toMatch(banner)
    expect(notify).toHaveBeenCalledWith(...toast)
  })

  it('resolves the active incident after confirmation', async () => {
    const user = userEvent.setup()
    const server = serveIncidents({
      ...twoFaults,
      active: { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: false, hintsRevealed: 0 },
    })

    renderIncidents(autoConfirm)
    await screen.findByText(/Active: oom-kill/)
    await user.click(screen.getByRole('button', { name: /^resolve$/i }))
    await waitFor(() => expect(server.requests).toContain('POST /incidents/resolve'))
  })

  it('hides the fault identity in silent mode until resolved', async () => {
    // The server sends "(hidden)" in place of a silent incident's name.
    serveIncidents({
      ...twoFaults,
      active: { fault: '(hidden)', injectedAt: new Date().toISOString(), silent: true, hintsRevealed: 0 },
    })
    renderIncidents()
    expect(await screen.findByText(/hidden \(silent mode\)/i)).toBeInTheDocument()
    // The fault name is not revealed in the active console heading.
    expect(screen.queryByText(/Active: oom-kill/)).not.toBeInTheDocument()
  })

  it('hides an unresolved silent check name and detail in the incident console', async () => {
    const user = userEvent.setup()
    const active = { fault: '(hidden)', injectedAt: new Date().toISOString(), silent: true, hintsRevealed: 0 }
    const server = serveIncidents({ ...twoFaults, active })
    server.override('GET', '/incidents/status', 200, {
      active,
      resolved: false,
      check: { name: 'traffic-reaches-the-workload', pass: false, explanation: 'FAIL: default-deny-ingress' },
    })

    const { notify } = renderIncidents()
    await user.click(await screen.findByRole('button', { name: /check if resolved/i }))
    const shown = await screen.findByRole('status')
    expect(shown.textContent).toContain('Not resolved yet - keep digging, or: labctl incident hint.')
    expect(shown.textContent).not.toMatch(/traffic-reaches-the-workload|default-deny-ingress/)
    expect(notify).toHaveBeenCalledWith('info', 'Still broken', 'Not resolved yet - keep digging, or: labctl incident hint')
    expect(server.unanswered).toEqual([])
  })

  it('shows the detection check after a silent incident is resolved', async () => {
    const user = userEvent.setup()
    const active = { fault: '(hidden)', injectedAt: new Date().toISOString(), silent: true, hintsRevealed: 0 }
    const server = serveIncidents({ ...twoFaults, active })
    server.override('GET', '/incidents/status', 200, {
      active,
      resolved: true,
      check: { name: 'traffic-reaches-the-workload', pass: true },
    })

    renderIncidents()
    await user.click(await screen.findByRole('button', { name: /check if resolved/i }))
    expect(await screen.findByText((_, el) =>
      el?.classList.contains('banner-body') === true &&
      /Detection check traffic-reaches-the-workload passes - resolved/.test(el.textContent ?? ''))).toBeInTheDocument()
  })

  // Only what the user must install. The app the fault resolved from the binding
  // is what gets broken, not a prerequisite - listing it said content that runs
  // against any conforming app ran against one.
  it('surfaces a fault\'s prerequisites so required tools are visible before injecting', async () => {
    serveIncidents({
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

  it('opens a fault’s details and closes them on a click outside', async () => {
    serveContract()
    renderIncidents()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Details' }))
    expect(await screen.findByRole('dialog', { name: 'Demo fault details' })).toBeInTheDocument()
    await user.click(screen.getByRole('presentation'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('has no accessibility violations', async () => {
    serveIncidents(twoFaults)
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

// The server carries on with an inject or resolve the client stopped waiting
// for, so a client timeout is not a failure: the view follows the incident list
// until the change shows up, and reports a failure only if it never does.
describe('Incidents view when an inject or resolve request times out', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  const timedOut = () => Promise.reject(new RequestTimeoutError('Request timed out after 30s - the server may be busy or stuck'))
  const oomActive = { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: false, hintsRevealed: 0 }
  const withActive: IncidentList = { ...twoFaults, active: oomActive }
  const injectRow = () => within(screen.getByText('oom-kill').closest('.scenario-row') as HTMLElement)

  const cases: {
    name: string
    call: 'injectIncident' | 'injectRandomIncident' | 'resolveIncident'
    lists: IncidentList[]
    act: () => void
    busy: () => HTMLElement
    wait: number
    running: [string, string]
    result: [string, string, string]
  }[] = [
    {
      name: 'an inject that finishes',
      call: 'injectIncident',
      lists: [twoFaults, twoFaults, withActive],
      act: () => fireEvent.click(injectRow().getByRole('button', { name: /^inject$/i })),
      busy: () => injectRow().getByRole('button', { name: 'Injecting…' }),
      wait: 10_000,
      running: ['info', 'Inject still running'],
      result: ['success', 'Injected oom-kill into go-api', 'Diagnose it, then resolve.'],
    },
    {
      name: 'a random inject that finishes',
      call: 'injectRandomIncident',
      lists: [twoFaults, withActive],
      act: () => fireEvent.click(screen.getByRole('button', { name: /inject random/i })),
      busy: () => screen.getByRole('button', { name: 'Injecting…' }),
      wait: 5_000,
      running: ['info', 'Random inject still running'],
      result: ['success', 'Injected a random incident', 'Now solve: oom-kill.'],
    },
    {
      name: 'a resolve that finishes',
      call: 'resolveIncident',
      lists: [withActive, twoFaults],
      act: () => fireEvent.click(screen.getByRole('button', { name: /^resolve$/i })),
      busy: () => screen.getByRole('button', { name: 'Resolving…' }),
      wait: 5_000,
      running: ['info', 'Resolve still running'],
      result: ['success', 'Incident resolved', 'The active incident is cleared.'],
    },
    {
      name: 'an inject that never finishes',
      call: 'injectIncident',
      lists: [twoFaults],
      act: () => fireEvent.click(injectRow().getByRole('button', { name: /^inject$/i })),
      busy: () => injectRow().getByRole('button', { name: 'Injecting…' }),
      wait: 5 * 60_000 + 5_000,
      running: ['info', 'Inject still running'],
      result: ['error', 'Inject did not finish', 'Nothing changed in 5 minutes. The Command Output panel has what the server reported.'],
    },
  ]

  it.each(cases)('waits for $name instead of reporting a failure', async ({ call, lists, act, busy, wait, running, result }) => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const server = serveIncidents(lists[0])
    server.overrideEach('GET', '/incidents', 200, lists)
    vi.spyOn(api, call).mockImplementation(timedOut)

    const { notify } = renderIncidents(autoConfirm)
    await screen.findByText('Incident library (2)')
    act()

    await waitFor(() => expect(notify).toHaveBeenCalledWith(...running, expect.stringMatching(/Command Output panel/)))
    expect(busy()).toBeDisabled()
    expect(notify).not.toHaveBeenCalledWith('error', expect.anything(), expect.anything())

    await vi.advanceTimersByTimeAsync(wait)
    await waitFor(() => expect(notify).toHaveBeenCalledWith(...result))
    expect(notify).toHaveBeenCalledTimes(2)
  })
})

describe('Incidents view by role', () => {
  // canOperate is undefined when auth is off (the shell treats that as operator).
  const roles: [string, boolean | undefined, boolean][] = [
    ['a participant', false, false],
    ['an operator', true, true],
    ['auth turned off', undefined, true],
  ]

  it.each(roles)('shows inject controls for %s: %s', async (_label, canOperate, shown) => {
    serveIncidents(twoFaults)
    renderIncidents(vi.fn(), canOperate)
    await screen.findByText('oom-kill')
    // Covers the library's Inject buttons and the idle banner's Inject random.
    expect(screen.queryAllByRole('button', { name: /^Inject/ }).length > 0).toBe(shown)
  })

  it.each(roles)('shows the resolve control for %s: %s', async (_label, canOperate, shown) => {
    serveIncidents({
      faults: twoFaults.faults,
      active: { fault: 'oom-kill', injectedAt: new Date().toISOString(), silent: false, hintsRevealed: 0 },
    })
    renderIncidents(vi.fn(), canOperate)
    await screen.findByRole('button', { name: 'Check if resolved' })
    expect(screen.queryByRole('button', { name: 'Resolve' }) !== null).toBe(shown)
  })

  it('tells a participant why there is no inject control', async () => {
    serveIncidents(twoFaults)
    renderIncidents(vi.fn(), false)
    expect(await screen.findByText(/Only an operator can inject one/)).toBeInTheDocument()
  })
})
