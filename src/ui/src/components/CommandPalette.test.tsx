// SPDX-License-Identifier: Apache-2.0
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi } from 'vitest'
import { CommandPalette, type Command } from './CommandPalette'

function makeCommands(spy: (id: string) => void): Command[] {
  return [
    { id: 'dashboard', label: 'Go to Dashboard', hint: '/dashboard', run: () => spy('dashboard') },
    { id: 'scenarios', label: 'Go to Scenarios', hint: '/scenarios', run: () => spy('scenarios') },
    { id: 'results', label: 'Go to Results', hint: '/results', run: () => spy('results') },
  ]
}

describe('CommandPalette', () => {
  it('renders nothing when closed', () => {
    render(<CommandPalette open={false} onClose={() => {}} commands={makeCommands(() => {})} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('filters as you type and runs the chosen command', async () => {
    const user = userEvent.setup()
    const ran = vi.fn()
    const onClose = vi.fn()
    render(<CommandPalette open onClose={onClose} commands={makeCommands(ran)} />)

    const input = screen.getByRole('combobox')
    await user.type(input, 'scen')

    // Only the matching command remains.
    expect(screen.getByRole('option', { name: /Go to Scenarios/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /Go to Dashboard/ })).not.toBeInTheDocument()

    await user.keyboard('{Enter}')
    expect(ran).toHaveBeenCalledOnce()
    expect(ran).toHaveBeenCalledWith('scenarios')
    expect(onClose).toHaveBeenCalled()
  })

  it('moves the selection with arrow keys and runs it with Enter', async () => {
    const user = userEvent.setup()
    const ran = vi.fn()
    render(<CommandPalette open onClose={() => {}} commands={makeCommands(ran)} />)

    // First option is active by default; ↓ moves to the second (Scenarios).
    await user.keyboard('{ArrowDown}{Enter}')
    expect(ran).toHaveBeenCalledWith('scenarios')
  })

  it('closes on Escape without running anything', async () => {
    const user = userEvent.setup()
    const ran = vi.fn()
    const onClose = vi.fn()
    render(<CommandPalette open onClose={onClose} commands={makeCommands(ran)} />)

    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalled()
    expect(ran).not.toHaveBeenCalled()
  })

  it('shows a no-matches message for a query that hits nothing', async () => {
    const user = userEvent.setup()
    render(<CommandPalette open onClose={() => {}} commands={makeCommands(() => {})} />)
    await user.type(screen.getByRole('combobox'), 'zzzzz')
    expect(screen.getByText('No matches')).toBeInTheDocument()
  })
})
