// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { useApiQuery } from '../hooks/useApiQuery'
import { useRunLogs } from '../hooks/useRunLogs'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import { errMessage } from '../lib/errors'
import type { NotifyFn, RunStatus, RunSummary } from '../types'

interface RunsProps {
  notify: NotifyFn
}

const TERMINAL: RunStatus[] = ['succeeded', 'failed', 'cancelled', 'timed_out']
const isTerminal = (s: RunStatus) => TERMINAL.includes(s)

function statusVariant(s: RunStatus): 'running' | 'stopped' | 'pending' | 'category' {
  switch (s) {
    case 'succeeded': return 'running'
    case 'running':   return 'pending'
    case 'queued':    return 'pending'
    case 'failed':    return 'stopped'
    case 'timed_out': return 'stopped'
    default:          return 'category' // cancelled
  }
}

function relTime(iso?: string): string {
  if (!iso) return '—'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return '—'
  const d = Math.max(0, Date.now() - then)
  const s = Math.floor(d / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

function durationLabel(r: RunSummary): string {
  if (r.durationMs && r.durationMs > 0) {
    const s = r.durationMs / 1000
    return s < 1 ? `${r.durationMs}ms` : `${s.toFixed(s < 10 ? 1 : 0)}s`
  }
  if (r.status === 'running' && r.startedAt) {
    return `${Math.floor((Date.now() - new Date(r.startedAt).getTime()) / 1000)}s…`
  }
  return '—'
}

const STATUS_FILTERS = ['all', 'running', 'succeeded', 'failed', 'cancelled'] as const
type StatusFilter = typeof STATUS_FILTERS[number]

export function Runs({ notify }: RunsProps) {
  const queryClient = useQueryClient()
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const { data: runs = [], loading, loaded, loadError, refreshing, reload } = useApiQuery(
    ['runs', statusFilter],
    () => api.listRuns(statusFilter === 'all' ? undefined : statusFilter),
    { refetchInterval: 4000 },
  )

  // Keep a selection valid across refetches; default to the newest run once.
  const autoSelected = useRef(false)
  useEffect(() => {
    if (!autoSelected.current && runs.length > 0) {
      autoSelected.current = true
      setSelectedId(prev => prev ?? runs[0].id)
    }
  }, [runs])

  if (loading) return <div className="loading" role="status">Loading runs…</div>

  if (loadError && !loaded) {
    return <ErrorState title="Failed to load runs" message={loadError} onRetry={reload} retrying={refreshing} />
  }

  const selected = runs.find(r => r.id === selectedId) ?? null

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          <Icon name="alert-triangle" size={16} className="banner-icon" />
          <span className="banner-body">Refresh failed ({loadError}) — showing last known data.</span>
        </div>
      )}

      {/* What this view is for. */}
      <div className="banner banner-info" role="note">
        <Icon name="info" size={16} className="banner-icon" />
        <span className="banner-body">
          <strong>Runs</strong> is the execution log for everything the lab does. Each lab, platform,
          scenario or incident action becomes a recorded run with a step timeline and live output —
          select one to watch it stream, cancel a run in flight, or open a past run to see exactly where it failed.
        </span>
      </div>

      <div className="runs-grid">
        {/* Run list */}
        <div className="card">
          <div className="card-header">
            <span className="card-title">Runs ({runs.length})</span>
            <select
              className="select"
              value={statusFilter}
              aria-label="Filter runs by status"
              onChange={e => setStatusFilter(e.target.value as StatusFilter)}
            >
              {STATUS_FILTERS.map(s => <option key={s} value={s}>{s === 'all' ? 'All statuses' : s}</option>)}
            </select>
          </div>

          {runs.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon"><Icon name="runs" size={24} /></span>
              <div>No runs recorded yet.</div>
              <div className="empty-hint">
                Trigger any action — activate a scenario, inject an incident, install a component, or run <code>labctl lab up</code> — and it shows up here with its live log.
              </div>
            </div>
          ) : (
            <ul className="run-list">
              {runs.map(r => (
                <li key={r.id}>
                  <button
                    type="button"
                    className={`run-row${r.id === selectedId ? ' run-row-active' : ''}`}
                    aria-current={r.id === selectedId}
                    onClick={() => setSelectedId(r.id)}
                  >
                    <span className="run-row-main">
                      <span className="run-row-title">{r.kind}{r.target ? ` · ${r.target}` : ''}</span>
                      <span className="run-row-sub">{relTime(r.startedAt ?? r.queuedAt)}</span>
                    </span>
                    <Badge variant={statusVariant(r.status)}>{r.status}</Badge>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* Detail */}
        {selected
          ? <RunDetailPanel key={selected.id} runId={selected.id} summary={selected} notify={notify} onChanged={() => queryClient.invalidateQueries({ queryKey: ['runs'] })} />
          : <div className="card"><div className="empty-state">Select a run to see its timeline and output.</div></div>}
      </div>
    </>
  )
}

function RunDetailPanel({ runId, summary, notify, onChanged }: {
  runId: string
  summary: RunSummary
  notify: NotifyFn
  onChanged: () => void
}) {
  const [cancelling, setCancelling] = useState(false)
  const terminal = isTerminal(summary.status)

  const { data: detail } = useApiQuery(
    ['runs', runId],
    () => api.getRun(runId),
    { refetchInterval: terminal ? undefined : 3000 },
  )
  const { lines, done, error: logError } = useRunLogs(runId)

  const logRef = useRef<HTMLPreElement>(null)
  useEffect(() => {
    if (logRef.current && !done) logRef.current.scrollTop = logRef.current.scrollHeight
  }, [lines, done])

  async function onCancel() {
    setCancelling(true)
    try {
      await api.cancelRun(runId)
      notify('info', 'Cancellation requested', `Run ${runId}`)
      onChanged()
    } catch (e) {
      notify('error', 'Cancel failed', errMessage(e))
    } finally {
      setCancelling(false)
    }
  }

  const steps = detail?.steps ?? []

  return (
    <div className="card">
      <div className="card-header">
        <span className="card-title truncate">{summary.kind}{summary.target ? ` · ${summary.target}` : ''}</span>
        <div className="card-tools">
          <Badge variant={statusVariant(summary.status)}>{summary.status}</Badge>
          {!terminal && (
            <button className="btn btn-sm btn-danger" onClick={onCancel} disabled={cancelling}>
              {cancelling ? 'Cancelling…' : 'Cancel'}
            </button>
          )}
        </div>
      </div>

      <div className="meta-grid">
        <Meta label="Run ID" value={summary.id} mono />
        <Meta label="Started" value={relTime(summary.startedAt ?? summary.queuedAt)} />
        <Meta label="Duration" value={durationLabel(summary)} />
        {summary.exitCode != null && <Meta label="Exit code" value={String(summary.exitCode)} />}
        {summary.actor && <Meta label="Actor" value={summary.actor} />}
      </div>

      {summary.error && (
        <div className="banner banner-warn mt-3" role="alert">
          <Icon name="alert-triangle" size={16} className="banner-icon" />
          <span className="banner-body">{summary.error}</span>
        </div>
      )}

      {steps.length > 0 && (
        <div className="mt-3">
          <div className="meta-label mt-2">Timeline</div>
          <ol className="timeline mt-2">
            {steps.map(s => (
              <li key={s.index} className="timeline-item">
                <Badge variant={s.status === 'succeeded' ? 'running' : s.status === 'failed' ? 'stopped' : 'pending'}>{s.status}</Badge>
                <span>{s.name}</span>
              </li>
            ))}
          </ol>
        </div>
      )}

      <div className="mt-3">
        <div className="row-between mt-2">
          <span className="meta-label">Output</span>
          {!terminal && !done && <span className="hint-text" aria-live="polite">streaming…</span>}
        </div>
        {logError && (
          <div className="banner banner-warn mt-2" role="alert">
            <Icon name="alert-triangle" size={16} className="banner-icon" />
            <span className="banner-body">{logError}</span>
          </div>
        )}
        <pre ref={logRef} className="run-output mt-2">
          {lines.length === 0
            ? <span className="run-output-placeholder">{terminal ? 'No output was recorded for this run.' : 'Waiting for output…'}</span>
            : lines.map(l => (
                <span key={l.seq} className={l.stream === 'stderr' ? 'out-stderr' : l.stream === 'system' ? 'out-system' : undefined}>
                  {l.stream === 'stderr' ? '! ' : l.stream === 'system' ? '* ' : ''}{l.text}{'\n'}
                </span>
              ))}
        </pre>
      </div>
    </div>
  )
}

function Meta({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="meta-item">
      <div className="meta-label">{label}</div>
      <div className={`meta-value${mono ? ' mono' : ''}`}>{value}</div>
    </div>
  )
}
