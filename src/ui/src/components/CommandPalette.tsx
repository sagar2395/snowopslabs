// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from 'react'
import { Icon, type IconName } from './Icon'

export interface Command {
  id: string
  label: string
  hint?: string
  /** Optional leading icon. */
  icon?: IconName
  /** Section heading the command groups under in the list. */
  group?: string
  run: () => void
}

interface CommandPaletteProps {
  open: boolean
  onClose: () => void
  commands: Command[]
}

/** A Cmd/Ctrl-K command palette: type to filter, ↑/↓ to move, Enter to run,
 *  Esc to close. It is a modal dialog with focus sent to the input on open and
 *  restored to the opener on close, so keyboard and screen-reader users get the
 *  same quick navigation as a mouse. */
export function CommandPalette({ open, onClose, commands }: CommandPaletteProps) {
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const opener = useRef<Element | null>(null)

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return commands
    return commands.filter(c =>
      c.label.toLowerCase().includes(q) || (c.hint ?? '').toLowerCase().includes(q))
  }, [commands, query])

  // Reset and focus when opened; restore focus to the opener when closed. The
  // input exists by the time this effect runs (it rendered this cycle), so we
  // focus it directly rather than deferring.
  useEffect(() => {
    if (open) {
      opener.current = document.activeElement
      setQuery('')
      setActive(0)
      inputRef.current?.focus()
    } else if (opener.current instanceof HTMLElement) {
      opener.current.focus()
    }
  }, [open])

  // Keep the active row in range as the filter narrows.
  useEffect(() => { setActive(a => Math.min(a, Math.max(0, filtered.length - 1))) }, [filtered.length])

  if (!open) return null

  function choose(index: number) {
    const cmd = filtered[index]
    if (!cmd) return
    onClose()
    cmd.run()
  }

  function onKeyDown(e: React.KeyboardEvent) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setActive(a => (a + 1) % Math.max(1, filtered.length))
        break
      case 'ArrowUp':
        e.preventDefault()
        setActive(a => (a - 1 + filtered.length) % Math.max(1, filtered.length))
        break
      case 'Enter':
        e.preventDefault()
        choose(active)
        break
      case 'Escape':
        e.preventDefault()
        onClose()
        break
    }
  }

  return (
    <div className="cmdk-overlay" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="cmdk-panel" role="dialog" aria-modal="true" aria-label="Command palette">
        <div className="cmdk-input-wrap">
          <Icon name="search" size={16} className="cmdk-search-icon" />
          <input
            ref={inputRef}
            className="cmdk-input"
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-controls="cmdk-list"
            aria-autocomplete="list"
            aria-activedescendant={filtered[active] ? `cmdk-opt-${filtered[active].id}` : undefined}
            placeholder="Jump to… (↑↓ to move, Enter to go, Esc to close)"
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
          />
        </div>
        <ul className="cmdk-list" id="cmdk-list" role="listbox" aria-label="Commands">
          {filtered.length === 0 ? (
            <li className="cmdk-empty" role="option" aria-disabled="true" aria-selected="false">No matches</li>
          ) : (
            filtered.map((c, i) => (
              <li
                key={c.id}
                id={`cmdk-opt-${c.id}`}
                role="option"
                aria-selected={i === active}
                className={`cmdk-opt${i === active ? ' active' : ''}`}
                onMouseEnter={() => setActive(i)}
                onMouseDown={e => { e.preventDefault(); choose(i) }}
              >
                {c.icon && <Icon name={c.icon} size={16} className="cmdk-opt-icon" />}
                <span className="cmdk-opt-label">{c.label}</span>
                {c.hint && <span className="cmdk-opt-hint">{c.hint}</span>}
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  )
}
