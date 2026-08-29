// SPDX-License-Identifier: Apache-2.0
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, beforeEach } from 'vitest'
import { ThemeToggle } from './ThemeToggle'
import { THEME_STORAGE_KEY } from '../lib/theme'

describe('ThemeToggle', () => {
  beforeEach(() => {
    try { localStorage.clear() } catch { /* ignore */ }
    document.documentElement.removeAttribute('data-theme')
  })

  it('exposes the three themes as an accessible radio group', () => {
    render(<ThemeToggle />)
    expect(screen.getByRole('radiogroup', { name: /theme/i })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Light theme' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'System theme' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Dark theme' })).toBeInTheDocument()
  })

  it('selecting a theme checks it, persists it, and applies it to the document', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await user.click(screen.getByRole('radio', { name: 'Light theme' }))

    expect(screen.getByRole('radio', { name: 'Light theme' })).toHaveAttribute('aria-checked', 'true')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')

    await user.click(screen.getByRole('radio', { name: 'Dark theme' }))
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
  })
})
