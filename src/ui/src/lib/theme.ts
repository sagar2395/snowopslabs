// SPDX-License-Identifier: Apache-2.0
//
// Theme resolution shared by the no-flash inline bootstrap (index.html) and the
// React useTheme hook. Kept dependency-free so the same logic can run before
// React mounts.

export type Theme = 'light' | 'dark' | 'system'

export const THEME_STORAGE_KEY = 'snowops-theme'

/** A Theme guard so a stray localStorage value can't put us in a bad state. */
export function isTheme(v: unknown): v is Theme {
  return v === 'light' || v === 'dark' || v === 'system'
}

/** readStoredTheme returns the saved preference, defaulting to 'system'.
 *  Wrapped in try/catch because localStorage throws in some privacy modes. */
export function readStoredTheme(): Theme {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    if (isTheme(v)) return v
  } catch { /* storage unavailable — fall through to default */ }
  return 'system'
}

export function storeTheme(theme: Theme): void {
  try { localStorage.setItem(THEME_STORAGE_KEY, theme) } catch { /* ignore */ }
}

/** systemPrefersDark reports the OS colour-scheme preference (defaults to dark,
 *  matching the app's dark-first identity, when matchMedia is unavailable). */
export function systemPrefersDark(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return true
  return !window.matchMedia('(prefers-color-scheme: light)').matches
}

/** resolveTheme maps a preference to the concrete palette to apply. */
export function resolveTheme(theme: Theme): 'light' | 'dark' {
  if (theme === 'system') return systemPrefersDark() ? 'dark' : 'light'
  return theme
}

/** applyTheme stamps the resolved palette onto <html data-theme>, which the CSS
 *  token overrides key off. 'dark' is the bare :root default, so we set the
 *  attribute only for light and clear it for dark to keep the DOM tidy. */
export function applyTheme(theme: Theme): void {
  if (typeof document === 'undefined') return
  const resolved = resolveTheme(theme)
  const root = document.documentElement
  if (resolved === 'light') {
    root.setAttribute('data-theme', 'light')
  } else {
    root.setAttribute('data-theme', 'dark')
  }
}
