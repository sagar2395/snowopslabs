import { useState, useEffect, useCallback } from 'react'
import { api } from '../api/client'
import type { LearnPathSummary, LearnPath, LearnProgress, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'

interface LearnProps {
  notify: NotifyFn
  refreshTick: number
}

export function Learn({ notify, refreshTick }: LearnProps) {
  const [paths, setPaths] = useState<LearnPathSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [selected, setSelected] = useState<{ path: LearnPath; progress: LearnProgress } | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [starting, setStarting] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const list = await api.listLearnPaths()
      setPaths(list ?? [])
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

  async function openPath(name: string) {
    setDetailLoading(true)
    setSelected(null)
    try {
      const [path, progress] = await Promise.all([
        api.getLearnPath(name),
        api.getLearnProgress(name),
      ])
      setSelected({ path, progress })
    } catch (e) {
      notify('error', 'Failed to load path', e instanceof Error ? e.message : String(e))
    } finally {
      setDetailLoading(false)
    }
  }

  async function startPath(name: string) {
    setStarting(name)
    try {
      const progress = await api.startLearnPath(name)
      notify('success', `Started "${name}"`, 'Work through the modules in order.')
      if (selected?.path.name === name) {
        setSelected(prev => prev ? { ...prev, progress } : null)
      }
      load()
    } catch (e) {
      notify('error', 'Failed to start path', e instanceof Error ? e.message : String(e))
    } finally {
      setStarting(null)
    }
  }

  function progressPct(s: LearnPathSummary) {
    if (!s.moduleCount) return 0
    return Math.round((s.completedCount / s.moduleCount) * 100)
  }

  function moduleStatus(progress: LearnProgress, idx: number) {
    if (!progress.started) return 'locked'
    if (progress.completed?.includes(idx)) return 'done'
    if (idx === progress.nextIdx) return 'next'
    return 'pending'
  }

  if (loading) return <div className="loading" role="status">Loading learning paths…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load learning paths"
        message={loadError}
        onRetry={() => { setRefreshing(true); load() }}
        retrying={refreshing}
      />
    )
  }

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>Retry</button>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Learning Paths ({paths.length})</span>
          <button className="btn btn-sm" onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {paths.length === 0 ? (
          <div className="empty-state">
            No learning paths found.
            <div className="empty-hint">Paths are discovered from <code>learn/&lt;name&gt;/path.yaml</code>.</div>
          </div>
        ) : (
          paths.map(p => {
            const pct = progressPct(p)
            const done = p.completedCount === p.moduleCount && p.moduleCount > 0
            return (
              <div key={p.name} className="scenario-row">
                <div className="scenario-info">
                  <div className="scenario-name">{p.displayName || p.name}</div>
                  {p.description && (
                    <div className="scenario-desc">
                      {p.description.length > 120 ? p.description.slice(0, 120) + '…' : p.description}
                    </div>
                  )}
                  <div className="scenario-tags" style={{ gap: 6, marginTop: 6 }}>
                    {(p.tags || []).map(tag => (
                      <Badge key={tag} variant="category">{tag}</Badge>
                    ))}
                    <span style={{ color: 'var(--muted)', fontSize: 12 }}>
                      {p.moduleCount} modules · ~{p.estimatedMinutes} min
                    </span>
                  </div>
                  {p.moduleCount > 0 && (
                    <div style={{ marginTop: 8 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <div style={{ flex: 1, height: 6, background: 'var(--surface)', borderRadius: 3, overflow: 'hidden' }}>
                          <div style={{ width: `${pct}%`, height: '100%', background: done ? 'var(--success)' : 'var(--accent)', transition: 'width 0.3s' }} />
                        </div>
                        <span style={{ fontSize: 12, color: 'var(--muted)', minWidth: 40 }}>{pct}%</span>
                      </div>
                      <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>
                        {p.completedCount}/{p.moduleCount} completed
                      </div>
                    </div>
                  )}
                </div>
                <Badge variant={done ? 'running' : p.completedCount > 0 ? 'category' : 'stopped'}>
                  {done ? 'Complete' : p.completedCount > 0 ? 'In Progress' : 'Not Started'}
                </Badge>
                <div className="scenario-actions">
                  {p.completedCount === 0 && (
                    <button
                      className="btn btn-sm btn-primary"
                      disabled={starting === p.name}
                      onClick={() => startPath(p.name)}
                    >
                      {starting === p.name ? 'Starting…' : 'Start'}
                    </button>
                  )}
                  <button className="btn btn-sm" onClick={() => openPath(p.name)}>
                    {detailLoading ? 'Loading…' : 'Details'}
                  </button>
                </div>
              </div>
            )
          })
        )}
      </div>

      {/* Path detail modal */}
      {(detailLoading || selected) && (
        <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget && !detailLoading) setSelected(null) }}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-label="Learning path details" style={{ maxWidth: 640 }}>
            <button className="modal-close" aria-label="Close" onClick={() => setSelected(null)}>×</button>

            {detailLoading ? (
              <div className="loading" role="status">Loading…</div>
            ) : selected ? (
              <>
                <div className="modal-header">
                  <h2>{selected.path.displayName || selected.path.name}</h2>
                  <div className="modal-meta">
                    {(selected.path as LearnPath & { tags?: string[] }).tags?.map((t: string) => (
                      <Badge key={t} variant="category">{t}</Badge>
                    ))}
                  </div>
                </div>

                {selected.path.description && (
                  <div className="modal-section">
                    <p>{selected.path.description}</p>
                  </div>
                )}

                <div className="modal-section">
                  <h3>Modules</h3>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {selected.path.modules.map((m, idx) => {
                      const status = moduleStatus(selected.progress, idx)
                      return (
                        <div
                          key={m.name}
                          style={{
                            display: 'flex', alignItems: 'center', gap: 12,
                            padding: '8px 12px', borderRadius: 6,
                            background: status === 'next' ? 'var(--surface-raised, var(--surface))' : 'transparent',
                            border: `1px solid ${status === 'next' ? 'var(--accent)' : 'var(--border)'}`,
                            opacity: status === 'locked' ? 0.5 : 1,
                          }}
                        >
                          <span style={{ fontSize: 16, minWidth: 20 }}>
                            {status === 'done' ? '✓' : status === 'next' ? '▶' : String(idx + 1)}
                          </span>
                          <div style={{ flex: 1 }}>
                            <div style={{ fontWeight: 500 }}>{m.displayName || m.name}</div>
                            <div style={{ fontSize: 12, color: 'var(--muted)' }}>
                              {m.action.type} · {m.action.ref}
                            </div>
                          </div>
                          <Badge variant={status === 'done' ? 'running' : status === 'next' ? 'category' : 'stopped'}>
                            {status === 'done' ? 'Done' : status === 'next' ? 'Next' : status === 'locked' ? 'Locked' : 'Pending'}
                          </Badge>
                        </div>
                      )
                    })}
                  </div>
                </div>

                <div className="card-footer">
                  {!selected.progress.started && (
                    <button
                      className="btn btn-primary"
                      disabled={starting === selected.path.name}
                      onClick={() => startPath(selected.path.name)}
                    >
                      {starting === selected.path.name ? 'Starting…' : 'Start Path'}
                    </button>
                  )}
                  <button className="btn" onClick={() => setSelected(null)}>Close</button>
                </div>
              </>
            ) : null}
          </div>
        </div>
      )}
    </>
  )
}
