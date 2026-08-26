import { useRef, useState, useEffect } from 'react'
import type { LogEntry } from '../types'

interface LogPanelProps {
  entries: LogEntry[]
  onClear: () => void
}

export function LogPanel({ entries, onClear }: LogPanelProps) {
  const [open, setOpen] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [panelHeight, setPanelHeight] = useState(260)
  const bodyRef = useRef<HTMLDivElement>(null)
  const dragging = useRef(false)
  const dragStart = useRef({ y: 0, h: 0 })

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
      <div
        className="log-panel-header"
        onClick={() => setOpen(o => !o)}
        onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpen(o => !o) } }}
        role="button"
        tabIndex={0}
        aria-expanded={open}
        aria-label="Toggle command output panel"
      >
        <span className="log-panel-title">
          Command Output {entries.length > 0 ? `(${entries.length})` : ''}
        </span>
        <div className="log-panel-controls" onClick={e => e.stopPropagation()}>
          <button className="log-ctrl-btn" onClick={onClear}>Clear</button>
          <button
            className={`log-ctrl-btn${autoScroll ? ' active' : ''}`}
            aria-pressed={autoScroll}
            onClick={() => setAutoScroll(a => !a)}
          >
            Auto-scroll
          </button>
          <button
            className="log-toggle"
            aria-label={open ? 'Collapse output panel' : 'Expand output panel'}
            onClick={() => setOpen(o => !o)}
          >
            {open ? '▼' : '▲'}
          </button>
        </div>
      </div>

      {open && (
        <div className="log-body" ref={bodyRef}>
          {entries.map(e => (
            <div key={e.id} className={`log-line log-${e.level}`}>
              <span className="log-ts">{e.ts}</span>
              {e.text}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
