// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SnippetList } from './SnippetList'
import type { WorkloadBinding } from '../types'

const echo: WorkloadBinding = { app: 'echo-server', namespace: 'echo-server', service: '', port: '8080', metric: '' }

describe('SnippetList', () => {
  it('names the app the bodies were rendered for, and explains script flags only when there are scripts', () => {
    const { rerender } = render(
      <SnippetList snippets={[{ label: 'deploy', path: 'manifests/dev.yaml', yaml: 'kind: Deployment' }]} notify={vi.fn()} workload={echo} />,
    )
    expect(screen.getByText(/shown for/i)).toHaveTextContent('Shown for echo-server in namespace echo-server.')
    expect(screen.queryByText(/--app/)).not.toBeInTheDocument()

    rerender(<SnippetList snippets={[{ label: 'tool', path: 'scripts/tool.sh', yaml: 'echo hi' }]} notify={vi.fn()} workload={echo} />)
    expect(screen.getByText('--app')).toBeInTheDocument()
  })

  it('marks an exercise and copies its ready-to-run command', async () => {
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    const notify = vi.fn()
    render(
      <SnippetList
        notify={notify}
        snippets={[{ label: 'ScaledObject', yaml: 'kind: ScaledObject', exercise: true, applyCommand: "kubectl apply -f - <<'EOF'\nkind: ScaledObject\nEOF" }]}
      />,
    )

    expect(screen.getByText('You apply this')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /copy apply command/i }))
    expect(writeText).toHaveBeenCalledWith("kubectl apply -f - <<'EOF'\nkind: ScaledObject\nEOF")
    expect(notify).toHaveBeenCalledWith('success', 'Copied the apply command', '')
  })

  it('shows no exercise marker or apply button on a reference snippet', () => {
    render(<SnippetList notify={vi.fn()} snippets={[{ label: 'installed', yaml: 'a: 1\nb: 2\n' }]} />)
    expect(screen.queryByText('You apply this')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /apply command/i })).not.toBeInTheDocument()
    expect(screen.getByText('2 lines')).toBeInTheDocument()
  })

  it('reports a clipboard failure instead of claiming success', async () => {
    const user = userEvent.setup()
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('denied'))
    const notify = vi.fn()
    render(<SnippetList notify={notify} snippets={[{ label: 'x', yaml: 'a: 1' }]} />)
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(notify).toHaveBeenCalledWith('error', 'Copy failed', expect.any(String))
  })
})
