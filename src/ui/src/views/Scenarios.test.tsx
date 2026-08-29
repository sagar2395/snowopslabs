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
  api: { listScenarios: vi.fn(), getScenario: vi.fn(), scenarioUp: vi.fn(), scenarioDown: vi.fn() },
}))
import { api } from '../api/client'
const mockApi = api as unknown as { listScenarios: Mock; getScenario: Mock; scenarioUp: Mock; scenarioDown: Mock }

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
    await waitFor(() => expect(mockApi.scenarioUp).toHaveBeenCalledWith('gitops-cicd'))
  })
})
