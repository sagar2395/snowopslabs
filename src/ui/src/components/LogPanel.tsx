import { useRef, useState, useEffect } from 'react'
import type { LogEntry } from '../types'
import { Icon } from './Icon'

interface LogPanelProps {
  entries: LogEntry[]
  onClear: () => void
}

const COLLAPSED_H = 40

export function LogPanel({ entries, onClear }: LogPanelProps) {
  const [open, setOpen] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [panelHeight, setPanelHeight] = useState(260)
  const bodyRef = useRef<HTMLDivElement>(null)
  const dragging = useRef(false)
  const dragStart = useRef({ y: 0, h: 0 })

  // Publish the panel's live height to a CSS variable so .main-content reserves
  // exactly the space the panel occupies — no dead gap when collapsed, no
  // content hidden behind it when expanded. (Fixes the old static 320px pad.)
  const effectiveHeight = open ? panelHeight : COLLAPSED_H
  useEffect(() => {
    document.documentElement.style.setProperty('--log-h', `${effectiveHeight}px`)
    return () => { document.documentElement.style.setProperty('--log-h', `${COLLAPSED_H}px`) }
  }, [effectiveHeight])

  // Auto-scroll on new entries
  useEffect(() => {
    if (autoScroll && bodyRef.current) {
      bodyRef.current.scrollTop = bodyRef.current.scrollHeight
    }
  }, [entries, autoScroll])

  // Auto-open when the first log entry arrives
  useEffect(() => {
    if (entries.length > 0 && !open) setOpen(true)
  }, [entries.length, open])

  // Resize handle
  function onMouseDown(e: React.MouseEvent) {
    if (!open) return
    dragging.current = true
    dragStart.current = { y: e.clientY, h: panelHeight }
    document.body.style.cursor = 'ns-resize'
    document.body.style.userSelect = 'none'
  }

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!dragging.current) return
      const delta = dragStart.current.y - e.clientY
      const h = Math.max(80, Math.min(Math.floor(window.innerHeight * 0.7), dragStart.current.h + delta))
      setPanelHeight(h)
    }
    function onUp() {
      dragging.current = false
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    return () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
    }
  }, [])

  return (
    <div
      className={`log-panel${open ? '' : ' log-panel-collapsed'}`}
      style={open ? { height: panelHeight } : undefined}
    >
      {open && (
        <div className="log-resize-handle" onMouseDown={onMouseDown} />
      )}
      {/* The title itself is the toggle button; the controls are siblings, not
          nested inside a button (which would be an a11y nested-interactive
          violation). */}
      <div className="log-panel-header">
        <button
          type="button"
          className="log-panel-titlebtn"
          onClick={() => setOpen(o => !o)}
          aria-expanded={open}
          aria-controls="log-panel-body"
        >
          <Icon name="terminal" size={15} />
          <span className="log-panel-title">
            Command Output {entries.length > 0 ? `(${entries.length})` : ''}
          </span>
        </button>
        <div className="log-panel-controls">
          <button className="log-ctrl-btn" onClick={onClear}>Clear</button>
          <button
            className={`log-ctrl-btn${autoScroll ? ' active' : ''}`}
            aria-pressed={autoScroll}
            onClick={() => setAutoScroll(a => !a)}
          >
            Auto-scroll
          </button>
          <button
            type="button"
            className="log-toggle"
            aria-label={open ? 'Collapse output panel' : 'Expand output panel'}
            aria-expanded={open}
            aria-controls="log-panel-body"
            onClick={() => setOpen(o => !o)}
          >
            <Icon name={open ? 'chevron-down' : 'chevron-up'} size={16} />
          </button>
        </div>
      </div>

      {open && (
        <div className="log-body" id="log-panel-body" ref={bodyRef}>
          {entries.map(e => (
            <div key={e.id} className={`log-line log-${e.level}`}>
              <span className="log-ts">{e.ts}</span>
              <span>{e.text}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
