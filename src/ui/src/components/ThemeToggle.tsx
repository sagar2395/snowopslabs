// SPDX-License-Identifier: Apache-2.0
import type { Theme } from '../lib/theme'
import { useTheme } from '../hooks/useTheme'
import { Icon, type IconName } from './Icon'

const OPTIONS: { value: Theme; label: string; icon: IconName }[] = [
  { value: 'light',  label: 'Light theme',  icon: 'sun' },
  { value: 'system', label: 'System theme', icon: 'monitor' },
  { value: 'dark',   label: 'Dark theme',   icon: 'moon' },
]

/** A compact three-way theme control (light / system / dark). It is a radio
 *  group so the whole control is one tab stop and arrow keys move between
 *  options — the native semantics screen readers already announce. */
export function ThemeToggle() {
  const { theme, setTheme } = useTheme()
  return (
    <div className="theme-toggle" role="radiogroup" aria-label="Colour theme">
      {OPTIONS.map(o => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={theme === o.value}
          aria-label={o.label}
          title={o.label}
          className={`theme-opt${theme === o.value ? ' active' : ''}`}
          onClick={() => setTheme(o.value)}
        >
          <Icon name={o.icon} size={15} />
        </button>
      ))}
    </div>
  )
}
