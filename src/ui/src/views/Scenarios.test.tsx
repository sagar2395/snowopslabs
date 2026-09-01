// SPDX-License-Identifier: Apache-2.0
//
// Scenarios view: activation shows a requirements preview (what it installs +
// what it needs) before anything runs, so nothing is a surprise. The api
// boundary is mocked (see Traffic.test.tsx for why).
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { Scenarios } from './Scenarios'
import type { Scenario } from '../types'
import type { ConfirmRequest } from '../components/ConfirmDialog'

vi.mock('../api/client', () => ({
  api: { listScenarios: vi.fn(), getScenario: vi.fn(), scenarioUp: vi.fn(), scenarioDown: vi.fn(), scenarioVerify: vi.fn() },
}))
import { api } from '../api/client'
const mockApi = api as unknown as { listScenarios: Mock; getScenario: Mock; scenarioUp: Mock; scenarioDown: Mock; scenarioVerify: Mock }

const gitops: Scenario = {
  name: 'gitops-cicd', displayName: 'GitOps & CI/CD', description: 'ArgoCD GitOps',
  category: 'delivery', active: false,
  prerequisites: { platform: ['ingress'], apps: [] },
  components: [
    { name: 'argocd', type: 'helm', namespace: 'argocd' },
    { name: 'argocd-apps', type: 'manifest', namespace: 'argocd' },
  ],
}

function renderScenarios() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  // requestConfirm is owned by the app shell; capture the request so we can
  // assert the preview and fire the confirm ourselves.
  let captured: ConfirmRequest | null = null
  const requestConfirm = (r: ConfirmRequest) => { captured = r }
  render(
    <QueryClientProvider client={client}>
      <Scenarios notify={vi.fn()} requestConfirm={requestConfirm} />
    </QueryClientProvider>,
  )
  return { getConfirm: () => captured }
}

describe('Scenarios view — activation preview', () => {
  beforeEach(() => vi.clearAllMocks())

  it('previews what a scenario installs and requires, then activates on confirm', async () => {
    const user = userEvent.setup()
    mockApi.listScenarios.mockResolvedValue([{ ...gitops }])
    mockApi.getScenario.mockResolvedValue(gitops)
    mockApi.scenarioUp.mockResolvedValue({ jobId: 'j1', status: 'accepted' })

    const { getConfirm } = renderScenarios()
    await user.click(await screen.findByRole('button', { name: /^activate$/i }))

    // Activation fetches detail and raises a confirm with the requirements.
    await waitFor(() => expect(mockApi.getScenario).toHaveBeenCalledWith('gitops-cicd'))
    await waitFor(() => expect(getConfirm()).not.toBeNull())
    const req = getConfirm()!
    render(<div>{req.message as React.ReactNode}</div>)
    expect(screen.getByText(/This will install/i)).toBeInTheDocument()
    expect(screen.getByText('argocd')).toBeInTheDocument()
    expect(screen.getByText(/Requires/i)).toBeInTheDocument()
    expect(within(screen.getByText(/Requires/i).parentElement as HTMLElement).getByText('ingress')).toBeInTheDocument()

    // scenarioUp only fires once the user confirms.
    expect(mockApi.scenarioUp).not.toHaveBeenCalled()
    req.onConfirm()
    // A scenario with no parameters activates with an empty override map.
    await waitFor(() => expect(mockApi.scenarioUp).toHaveBeenCalledWith('gitops-cicd', {}))
  })
})

describe('Scenarios view — parameters', () => {
  beforeEach(() => vi.clearAllMocks())

  const withParams: Scenario = {
    ...gitops,
    parameters: [
      { name: 'Threshold', displayName: 'RPS per replica', default: '25', type: 'int', min: 1, max: 500 },
    ],
  }

  it('shows tunable parameters seeded to defaults and submits overrides on confirm', async () => {
    const user = userEvent.setup()
    mockApi.listScenarios.mockResolvedValue([{ ...withParams }])
    mockApi.getScenario.mockResolvedValue(withParams)
    mockApi.scenarioUp.mockResolvedValue({ jobId: 'j1', status: 'accepted' })

    const { getConfirm } = renderScenarios()
    await user.click(await screen.findByRole('button', { name: /^activate$/i }))
    await waitFor(() => expect(getConfirm()).not.toBeNull())
    const req = getConfirm()!
    render(<div>{req.message as React.ReactNode}</div>)

    const input = screen.getByLabelText(/RPS per replica/i) as HTMLInputElement
    expect(input.value).toBe('25') // seeded to the declared default

    await user.clear(input)
    await user.type(input, '15')

    req.onConfirm()
    await waitFor(() => expect(mockApi.scenarioUp).toHaveBeenCalledWith('gitops-cicd', { Threshold: '15' }))
  })
})

describe('Scenarios view — detail modal teaches implementation', () => {
  beforeEach(() => vi.clearAllMocks())

  const rich: Scenario = {
    ...gitops,
    objectives: ['Declare a KEDA ScaledObject'],
    snippets: [{ label: 'The ScaledObject', path: 'manifests/scaledobject.yaml', yaml: 'kind: ScaledObject\nthreshold: 25' }],
    checks: [{ name: 'keda-hpa-created', type: 'kubectl', resource: 'hpa/keda-hpa-go-api', operator: 'exists' }],
  }

  it('renders objectives, the implementation snippet, and the success checks', async () => {
    const user = userEvent.setup()
    mockApi.listScenarios.mockResolvedValue([{ ...rich }])
    mockApi.getScenario.mockResolvedValue(rich)

    renderScenarios()
    await user.click(await screen.findByRole('button', { name: /details/i }))

    // Objectives, snippet content (the actual manifest), and check assertion all show.
    expect(await screen.findByText(/What you'll learn/i)).toBeInTheDocument()
    expect(screen.getByText('Declare a KEDA ScaledObject')).toBeInTheDocument()
    expect(screen.getByText(/How it's implemented/i)).toBeInTheDocument()
    expect(screen.getByText(/kind: ScaledObject/)).toBeInTheDocument()
    expect(screen.getByText('keda-hpa-created')).toBeInTheDocument()
    expect(screen.getByText(/hpa\/keda-hpa-go-api/)).toBeInTheDocument()
  })
})

describe('Scenarios view — verify', () => {
  beforeEach(() => vi.clearAllMocks())

  const active: Scenario = { ...gitops, active: true }

  it('is disabled while the scenario is inactive', async () => {
    mockApi.listScenarios.mockResolvedValue([{ ...gitops }]) // inactive
    renderScenarios()
    const verifyBtn = await screen.findByRole('button', { name: /verify/i })
    expect(verifyBtn).toBeDisabled()
  })

  it('runs the checks and renders the per-check pass/fail breakdown', async () => {
    const user = userEvent.setup()
    mockApi.listScenarios.mockResolvedValue([active])
    mockApi.scenarioVerify.mockResolvedValue({
      scenario: 'gitops-cicd',
      passed: false,
      results: [
        { name: 'argocd-ready', type: 'kubectl', pass: true, got: '1', want: '>= 1' },
        { name: 'app-synced', type: 'kubectl', pass: false, got: '0', want: '>= 1' },
      ],
    })

    renderScenarios()
    await user.click(await screen.findByRole('button', { name: /verify/i }))

    await waitFor(() => expect(mockApi.scenarioVerify).toHaveBeenCalledWith('gitops-cicd'))
    // Both checks render with their names and PASS/FAIL states.
    expect(await screen.findByText('argocd-ready')).toBeInTheDocument()
    expect(screen.getByText('app-synced')).toBeInTheDocument()
    expect(screen.getByText('PASS')).toBeInTheDocument()
    expect(screen.getByText('FAIL')).toBeInTheDocument()
  })
})
