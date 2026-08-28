// SPDX-License-Identifier: Apache-2.0
//
// Learn view behaviour (W7-T01): a learner can open a path and progress through
// its modules to completion entirely in the UI, without dropping to the CLI.
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { axe, toHaveNoViolations } from 'jest-axe'
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest'
import { Learn } from './Learn'
import type { LearnPath, LearnPathSummary, LearnProgress } from '../types'

expect.extend(toHaveNoViolations)

vi.mock('../api/client', () => ({
  api: {
    listLearnPaths: vi.fn(),
    getLearnPath: vi.fn(),
    getLearnProgress: vi.fn(),
    startLearnPath: vi.fn(),
    completeLearnModule: vi.fn(),
  },
}))
import { api } from '../api/client'
const mockApi = api as unknown as {
  listLearnPaths: Mock
  getLearnPath: Mock
  getLearnProgress: Mock
  startLearnPath: Mock
  completeLearnModule: Mock
}

const summary: LearnPathSummary = {
  name: 'k8s-basics', displayName: 'K8s Basics', description: 'Learn the basics',
  tags: ['k8s'], estimatedMinutes: 20, moduleCount: 2, completedCount: 1,
}
const path: LearnPath = {
  name: 'k8s-basics', displayName: 'K8s Basics', description: 'Learn the basics', tags: ['k8s'],
  modules: [
    { name: 'm1', displayName: 'Module One', hasIntro: false, action: { type: 'command', ref: 'up' }, completed: true },
    { name: 'm2', displayName: 'Module Two', hasIntro: false, action: { type: 'command', ref: 'go' }, completed: false },
  ],
}
// Started, module 0 done, module 1 is next.
const progress: LearnProgress = { path: 'k8s-basics', started: true, completed: [0], total: 2, nextIdx: 1 }

function renderLearn() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const notify = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <Learn notify={notify} />
    </QueryClientProvider>,
  )
  return { notify }
}

describe('Learn view', () => {
  beforeEach(() => vi.clearAllMocks())

  it('completes the next module from the UI and reports finishing the path', async () => {
    const user = userEvent.setup()
    mockApi.listLearnPaths.mockResolvedValue([summary])
    mockApi.getLearnPath.mockResolvedValue(path)
    mockApi.getLearnProgress.mockResolvedValue(progress)
    // Completing the last module finishes the path (nextIdx: -1).
    mockApi.completeLearnModule.mockResolvedValue({ ...progress, completed: [0, 1], nextIdx: -1 })

    const { notify } = renderLearn()

    await user.click(await screen.findByRole('button', { name: /details/i }))

    const dialog = await screen.findByRole('dialog')
    // The next module offers an in-UI "Mark complete" action.
    const markButtons = within(dialog).getAllByRole('button', { name: /mark complete/i })
    await user.click(markButtons[0])

    await waitFor(() => expect(mockApi.completeLearnModule).toHaveBeenCalledWith('k8s-basics', 1))
    // Finishing the path surfaces a success notification and the complete badge.
    await waitFor(() => expect(notify).toHaveBeenCalledWith('success', 'Path complete', expect.any(String)))
    expect(within(screen.getByRole('dialog')).getByText(/path complete/i)).toBeInTheDocument()
  })

  it('has no accessibility violations with the detail modal open', async () => {
    const user = userEvent.setup()
    mockApi.listLearnPaths.mockResolvedValue([summary])
    mockApi.getLearnPath.mockResolvedValue(path)
    mockApi.getLearnProgress.mockResolvedValue(progress)

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { container } = render(
      <QueryClientProvider client={client}>
        <Learn notify={vi.fn()} />
      </QueryClientProvider>,
    )
    await user.click(await screen.findByRole('button', { name: /details/i }))
    await screen.findByRole('dialog')
    expect(await axe(container)).toHaveNoViolations()
  })

  it('offers a Start action for an unstarted path', async () => {
    const user = userEvent.setup()
    const fresh: LearnPathSummary = { ...summary, completedCount: 0 }
    mockApi.listLearnPaths.mockResolvedValue([fresh])
    mockApi.startLearnPath.mockResolvedValue({ path: 'k8s-basics', started: true, completed: [], total: 2, nextIdx: 0 })

    renderLearn()
    const startBtn = await screen.findByRole('button', { name: /^start$/i })
    await user.click(startBtn)
    await waitFor(() => expect(mockApi.startLearnPath).toHaveBeenCalledWith('k8s-basics'))
  })
})
