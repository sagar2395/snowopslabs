// SPDX-License-Identifier: Apache-2.0
//
// View-behaviour test. The api boundary is mocked here (not MSW) because the
// component layer runs in jsdom, where the client's AbortController-based fetch
// is incompatible with undici — the same reason the network-contract tests
// (api/client.test.ts) run in the node environment. This test asserts what the
// Traffic view *does* with the client, not the wire format, which client.test
// covers.
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { Traffic } from './Traffic'

vi.mock('../api/client', () => ({
  api: {
    getTraffic: vi.fn(),
    startTraffic: vi.fn(),
    stopTraffic: vi.fn(),
    listApps: vi.fn(),
  },
}))

// Import after the mock so we get the mocked module.
import { api } from '../api/client'
const mockApi = api as unknown as {
  getTraffic: Mock
  startTraffic: Mock
  stopTraffic: Mock
  listApps: Mock
}

// The traffic targets come from the apps API — each app's in-cluster address is
// the one it declares in its contract, not one the UI reconstructs.
const APPS = [
  { name: 'go-api', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true,
    serviceUrl: 'http://go-api.go-api.svc.cluster.local:8080/' },
  { name: 'java-api', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true,
    serviceUrl: 'http://java-api.team-a.svc.cluster.local:9090/' },
]

function renderTraffic() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const notify = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <Traffic notify={notify} />
    </QueryClientProvider>,
  )
  return { notify }
}

describe('Traffic view', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockApi.listApps.mockResolvedValue(APPS)
  })

  it('lists the k6 profiles and offers start/stop controls', async () => {
    mockApi.getTraffic.mockResolvedValue({ profiles: ['steady', 'spike', 'soak'] })
    renderTraffic()

    const select = await screen.findByRole('combobox', { name: /traffic profile/i })
    expect(select).toHaveValue('steady') // defaults to the first profile
    expect(screen.getByRole('option', { name: 'spike' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /start traffic/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /stop traffic/i })).toBeInTheDocument()
  })

  it('starts the selected profile with the chosen rps', async () => {
    mockApi.getTraffic.mockResolvedValue({ profiles: ['steady', 'spike'] })
    mockApi.startTraffic.mockResolvedValue({ jobId: 'job-1', status: 'accepted' })

    const user = userEvent.setup()
    const { notify } = renderTraffic()

    await screen.findByRole('combobox', { name: /traffic profile/i })
    await screen.findByRole('option', { name: /go-api/ })
    await user.selectOptions(screen.getByRole('combobox', { name: /traffic profile/i }), 'spike')
    await user.click(screen.getByRole('button', { name: /start traffic/i }))

    await waitFor(() =>
      expect(mockApi.startTraffic).toHaveBeenCalledWith({
        profile: 'spike',
        rps: 50,
        duration: undefined,
        target: 'http://go-api.go-api.svc.cluster.local:8080/',
      }))
    await waitFor(() =>
      expect(notify).toHaveBeenCalledWith('info', expect.stringMatching(/Traffic \(spike → go-api\) started/), expect.anything()))
  })

  it('stops traffic', async () => {
    mockApi.getTraffic.mockResolvedValue({ profiles: ['steady'] })
    mockApi.stopTraffic.mockResolvedValue({ jobId: 'job-2', status: 'accepted' })

    const user = userEvent.setup()
    renderTraffic()
    await screen.findByRole('button', { name: /stop traffic/i })
    await user.click(screen.getByRole('button', { name: /stop traffic/i }))
    await waitFor(() => expect(mockApi.stopTraffic).toHaveBeenCalled())
  })

  it('shows an empty state when no profiles exist', async () => {
    mockApi.getTraffic.mockResolvedValue({ profiles: [] })
    renderTraffic()
    expect(await screen.findByText(/No traffic profiles found/)).toBeInTheDocument()
  })

  it('shows a designed error state when the profiles fail to load', async () => {
    mockApi.getTraffic.mockRejectedValue(new Error('boom'))
    renderTraffic()
    expect(await screen.findByText(/Failed to load traffic profiles/)).toBeInTheDocument()
  })

  // A new app — or one on a non-default port — must appear as a target without
  // any UI change. The address is the app's own, never rebuilt by the client.
  it('offers every app the API reports, at its declared in-cluster address', async () => {
    mockApi.getTraffic.mockResolvedValue({ profiles: ['steady'] })
    mockApi.startTraffic.mockResolvedValue({ jobId: 'job-3', status: 'accepted' })

    const user = userEvent.setup()
    renderTraffic()

    const target = await screen.findByRole('combobox', { name: /traffic target/i })
    expect(await screen.findByRole('option', { name: /java-api/ })).toBeInTheDocument()

    await user.selectOptions(target, 'java-api')
    await user.click(screen.getByRole('button', { name: /start traffic/i }))

    await waitFor(() =>
      expect(mockApi.startTraffic).toHaveBeenCalledWith(expect.objectContaining({
        target: 'http://java-api.team-a.svc.cluster.local:9090/',
      })))
  })

  it('says so rather than silently targeting nothing when no app declares an address', async () => {
    mockApi.getTraffic.mockResolvedValue({ profiles: ['steady'] })
    mockApi.listApps.mockResolvedValue([])
    renderTraffic()

    await screen.findByRole('combobox', { name: /traffic target/i })
    expect(await screen.findByText(/No app declares an in-cluster address/i)).toBeInTheDocument()
  })

  // Load aimed at a workload nobody is looking at is worse than no default, and
  // the first app alphabetically is not the one the lab is working on.
  it('preselects the bound workload rather than the first app', async () => {
    mockApi.getTraffic.mockResolvedValue({
      profiles: ['steady'],
      workload: { app: 'go-api', namespace: 'go-api', service: 'go-api.go-api.svc.cluster.local', port: '8080', metric: 'm' },
    })
    mockApi.listApps.mockResolvedValue([
      { name: 'echo-server', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true, serviceUrl: 'http://echo-server.echo-server.svc.cluster.local:8080/' },
      { name: 'go-api', buildStrategy: 'docker', deployStrategy: 'helm', deployed: true, serviceUrl: 'http://go-api.go-api.svc.cluster.local:8080/' },
    ])
    renderTraffic()

    const target = await screen.findByRole('combobox', { name: /traffic target/i }) as HTMLSelectElement
    await waitFor(() => expect(target.value).toBe('go-api'))
    expect(await screen.findByText('http://go-api.go-api.svc.cluster.local:8080/')).toBeInTheDocument()
  })
})