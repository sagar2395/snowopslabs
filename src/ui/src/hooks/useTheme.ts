// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useState } from 'react'
import {
  type Theme, readStoredTheme, storeTheme, applyTheme, resolveTheme,
} from '../lib/theme'

/** useTheme owns the theme preference: it reads the stored value, applies it to
 *  the document, persists changes, and — while on 'system' — follows the OS
 *  colour-scheme live. Returns the preference, the concrete resolved palette,
 *  and a setter. */
export function useTheme(): {
  theme: Theme
  resolved: 'light' | 'dark'
  setTheme: (t: Theme) => void
} {
  const [theme, setThemeState] = useState<Theme>(() => readStoredTheme())

  const setTheme = useCallback((t: Theme) => {
    setThemeState(t)
    storeTheme(t)
    applyTheme(t)
  }, [])

  // Apply on mount (the inline bootstrap already did this pre-paint, but this
  // keeps DOM and state in sync after hydration / hot reload).
  useEffect(() => { applyTheme(theme) }, [theme])

  // While following the system, re-apply when the OS preference flips.
  useEffect(() => {
    if (theme !== 'system' || typeof window === 'undefined' || !window.matchMedia) return
    const mq = window.matchMedia('(prefers-color-scheme: light)')
    const onChange = () => applyTheme('system')
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [theme])

  return { theme, resolved: resolveTheme(theme), setTheme }
}
