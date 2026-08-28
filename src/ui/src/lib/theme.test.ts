// SPDX-License-Identifier: Apache-2.0
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import {
  isTheme, readStoredTheme, storeTheme, resolveTheme, applyTheme,
  THEME_STORAGE_KEY,
} from './theme'

// jsdom provides localStorage and document; matchMedia is stubbed per test.
function stubMatchMedia(prefersLight: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('light') ? prefersLight : !prefersLight,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }))
}

describe('theme', () => {
  beforeEach(() => {
    try { localStorage.clear() } catch { /* ignore */ }
    document.documentElement.removeAttribute('data-theme')
  })
  afterEach(() => { vi.unstubAllGlobals() })

  it('guards the Theme union', () => {
    expect(isTheme('light')).toBe(true)
    expect(isTheme('dark')).toBe(true)
    expect(isTheme('system')).toBe(true)
    expect(isTheme('neon')).toBe(false)
    expect(isTheme(null)).toBe(false)
  })

  it('defaults to system when nothing is stored, and round-trips a stored value', () => {
    expect(readStoredTheme()).toBe('system')
    storeTheme('light')
    expect(readStoredTheme()).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
  })

  it('ignores a corrupt stored value', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'chartreuse')
    expect(readStoredTheme()).toBe('system')
  })

  it('resolves system to the OS preference', () => {
    stubMatchMedia(true) // prefers light
    expect(resolveTheme('system')).toBe('light')
    stubMatchMedia(false) // prefers dark
    expect(resolveTheme('system')).toBe('dark')
    // Explicit choices ignore the OS.
    expect(resolveTheme('light')).toBe('light')
    expect(resolveTheme('dark')).toBe('dark')
  })

  it('stamps data-theme onto the document root', () => {
    applyTheme('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    applyTheme('dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
  })
})
