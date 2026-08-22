import { useState, useEffect, useCallback } from 'react'
import { api } from '../api/client'
import type { ResultRecord, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'

interface ResultsProps {
  notify: NotifyFn
  refreshTick: number
}

const KIND_LABELS: Record<string, string> = {
  incident:  'Incident',
  challenge: 'Challenge',
  module:    'Learn Module',
}

function formatDate(iso: string) {
  try { return new Date(iso).toLocaleString() } catch { return iso }
}

function formatElapsed(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return s > 0 ? `${m}m ${s}s` : `${m}m`
}

function scoreVariant(score: number) {
  if (score < 0) return 'category'    // unscored (-1)
  if (score >= 80) return 'running'
  if (score >= 50) return 'category'
  return 'stopped'
}

function scoreLabel(score: number) {
  return score < 0 ? '—' : String(score)
}

const KINDS = ['all', 'incident', 'challenge', 'module'] as const
type KindFilter = typeof KINDS[number]

export function Results({ notify, refreshTick }: ResultsProps) {
  const [records, setRecords] = useState<ResultRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [kindFilter, setKindFilter] = useState<KindFilter>('all')

  const load = useCallback(async () => {
    try {
      const data = await api.getResults()
      // newest-first
      const sorted = [...(data ?? [])].sort((a, b) =>
        new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime()
      )
      setRecords(sorted)
      setLoadError(null)
      setLoaded(true)
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { load() }, [load])
  useEffect(() => { if (refreshTick > 0) load() }, [refreshTick, load])

  if (loading) return <div className="loading" role="status">Loading results…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load results"
        message={loadError}
        onRetry={() => { setRefreshing(true); load() }}
        retrying={refreshing}
      />
    )
  }

  const visible = kindFilter === 'all' ? records : records.filter(r => r.kind === kindFilter)

  // Simple stats
  const challenges = records.filter(r => r.kind === 'challenge' && r.score >= 0)
  const avgScore = challenges.length
    ? Math.round(challenges.reduce((s, r) => s + r.score, 0) / challenges.length)
    : null
  const incidents = records.filter(r => r.kind === 'incident')
  const modules = records.filter(r => r.kind === 'module')

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>Retry</button>
        </div>
      )}

      {/* Summary stats */}
      {records.length > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 12, marginBottom: 16 }}>
          {[
            { label: 'Total runs',       value: records.length },
            { label: 'Incidents',        value: incidents.length },
            { label: 'Challenges',       value: challenges.length },
            { label: 'Modules done',     value: modules.length },
            ...(avgScore != null ? [{ label: 'Avg challenge score', value: `${avgScore}` }] : []),
          ].map(s => (
            <div key={s.label} className="card" style={{ padding: '12px 16px', textAlign: 'center' }}>
              <div style={{ fontSize: 28, fontWeight: 700 }}>{s.value}</div>
              <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4 }}>{s.label}</div>
            </div>
          ))}
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">
            Run History ({visible.length}{kindFilter !== 'all' ? ` of ${records.length}` : ''})
          </span>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <select
              className="runtime-select"
              value={kindFilter}
              aria-label="Filter by kind"
              onChange={e => setKindFilter(e.target.value as KindFilter)}
            >
              <option value="all">All types</option>
              {KINDS.filter(k => k !== 'all').map(k => (
                <option key={k} value={k}>{KIND_LABELS[k] ?? k}</option>
              ))}
            </select>
            <button className="btn btn-sm" onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>
              {refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </div>

        {visible.length === 0 ? (
          <div className="empty-state">
            {records.length === 0
              ? <>No runs recorded yet. Complete an incident, challenge, or learn module to see history.</>
              : <>No {KIND_LABELS[kindFilter] ?? kindFilter} runs recorded yet.</>
            }
          </div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="modal-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Type</th>
                  <th>Name</th>
                  <th>Date</th>
                  <th>Duration</th>
                  <th>Score</th>
                  <th>Outcome</th>
                  {records.some(r => r.user) && <th>User</th>}
                </tr>
              </thead>
              <tbody>
                {visible.map((r, i) => (
                  <tr key={i}>
                    <td>
                      <Badge variant="category">{KIND_LABELS[r.kind] ?? r.kind}</Badge>
                    </td>
                    <td style={{ maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {r.name}
                    </td>
                    <td style={{ color: 'var(--muted)', fontSize: 12, whiteSpace: 'nowrap' }}>
                      {formatDate(r.startedAt)}
                    </td>
                    <td style={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>
                      {formatElapsed(r.elapsedSeconds)}
                    </td>
                    <td>
                      <Badge variant={scoreVariant(r.score)}>{scoreLabel(r.score)}</Badge>
                    </td>
                    <td>
                      <Badge variant={
                        r.outcome === 'passed' || r.outcome === 'resolved' || r.outcome === 'auto-resolved'
                          ? 'running'
                          : r.outcome === 'failed' || r.outcome === 'aborted'
                          ? 'stopped'
                          : 'category'
                      }>
                        {r.outcome}
                      </Badge>
                    </td>
                    {records.some(rec => rec.user) && (
                      <td style={{ color: 'var(--muted)', fontSize: 12 }}>{r.user ?? '—'}</td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  )
}
