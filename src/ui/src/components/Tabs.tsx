// SPDX-License-Identifier: Apache-2.0
//
// Tabbed panel for detail modals. The detail views grew past a comfortable
// single scroll (a scenario can carry description, objectives, prerequisites,
// components, snippets, explore URLs/commands/tips and checks at once), so the
// sections are grouped into a handful of tabs and only the selected one renders.
//
// Follows the WAI-ARIA tabs pattern: roving tabindex, arrow/Home/End keys, and
// a focusable panel so a keyboard user can scroll the content they just opened.
import { useEffect, useId, useRef, useState } from 'react'

export interface TabItem {
  id: string
  label: string
  /** Optional count rendered as a pill — how much is behind the tab. */
  count?: number
  content: React.ReactNode
}

interface TabsProps {
  tabs: TabItem[]
  /** Names the tablist for screen readers, e.g. "GitOps & CI/CD sections". */
  label: string
}

export function Tabs({ tabs, label }: TabsProps) {
  const uid = useId()
  const [active, setActive] = useState(0)
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([])
  const panelRef = useRef<HTMLDivElement>(null)
  // Tabs are built from whatever the record carries, so the set can shrink
  // between records; keep the selection in range rather than blanking the panel.
  useEffect(() => {
    if (active >= tabs.length) setActive(0)
  }, [tabs.length, active])

  // Switching tabs starts a new document; carrying the old scroll offset over
  // drops the reader into the middle of it.
  useEffect(() => {
    let el = panelRef.current?.parentElement
    while (el) {
      const overflow = getComputedStyle(el).overflowY
      if (overflow === 'auto' || overflow === 'scroll') { el.scrollTop = 0; return }
      el = el.parentElement
    }
  }, [active])

  if (tabs.length === 0) return null
  if (tabs.length === 1) return <div className="tabpanel">{tabs[0].content}</div>

  const current = Math.min(active, tabs.length - 1)

  function select(i: number) {
    setActive(i)
    tabRefs.current[i]?.focus()
  }

  function onKeyDown(e: React.KeyboardEvent) {
    const last = tabs.length - 1
    if (e.key === 'ArrowRight') select(current === last ? 0 : current + 1)
    else if (e.key === 'ArrowLeft') select(current === 0 ? last : current - 1)
    else if (e.key === 'Home') select(0)
    else if (e.key === 'End') select(last)
    else return
    e.preventDefault()
  }

  return (
    <>
      <div className="tablist-wrap">
        <div className="tablist" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
          {tabs.map((t, i) => (
            <button
              key={t.id}
              ref={el => { tabRefs.current[i] = el }}
              type="button"
              role="tab"
              id={`${uid}-tab-${t.id}`}
              className="tab"
              aria-selected={i === current}
              aria-controls={`${uid}-panel-${t.id}`}
              tabIndex={i === current ? 0 : -1}
              onClick={() => setActive(i)}
            >
              {t.label}
              {t.count !== undefined && t.count > 0 && <span className="tab-count">{t.count}</span>}
            </button>
          ))}
        </div>
      </div>
      <div
        ref={panelRef}
        role="tabpanel"
        id={`${uid}-panel-${tabs[current].id}`}
        aria-labelledby={`${uid}-tab-${tabs[current].id}`}
        className="tabpanel"
        tabIndex={0}
      >
        {tabs[current].content}
      </div>
    </>
  )
}
