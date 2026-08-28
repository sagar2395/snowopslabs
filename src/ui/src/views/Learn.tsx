import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { LearnPathSummary, LearnPath, LearnProgress, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'

interface LearnProps {
  notify: NotifyFn
}

export function Learn({ notify }: LearnProps) {
  const { data: paths = [], loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.learnPaths, api.listLearnPaths)
  const [selected, setSelected] = useState<{ path: LearnPath; progress: LearnProgress } | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [openingPath, setOpeningPath] = useState<string | null>(null)
  const [starting, setStarting] = useState<string | null>(null)
  const [completingIdx, setCompletingIdx] = useState<number | null>(null)

  async function openPath(name: string) {
    setDetailLoading(true)
    setOpeningPath(name)
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
      setOpeningPath(null)
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

  // Completing a module is the core of the in-UI progression (W7-T01): the
  // learner works through a path end to end without dropping to the CLI. The
  // server returns the updated progress (nextIdx advances, -1 when finished),
  // which we fold straight into the open modal and mirror into the list.
  async function completeModule(name: string, idx: number) {
    setCompletingIdx(idx)
    try {
      const progress = await api.completeLearnModule(name, idx)
      setSelected(prev => (prev && prev.path.name === name ? { ...prev, progress } : prev))
      if (progress.nextIdx === -1) {
        notify('success', 'Path complete', `You finished "${name}". Nicely done.`)
      }
      load()
    } catch (e) {
      notify('error', 'Failed to complete module', e instanceof Error ? e.message : String(e))
    } finally {
      setCompletingIdx(null)
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
        onRetry={load}
        retrying={refreshing}
      />
    )
  }

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={load} disabled={refreshing}>Retry</button>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Learning Paths ({paths.length})</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
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
                  <button className="btn btn-sm" onClick={() => openPath(p.name)} disabled={openingPath === p.name}>
                    {openingPath === p.name ? 'Loading…' : 'Details'}
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
                    {selected.progress.started && (
                      <span style={{ color: 'var(--muted)', fontSize: 12 }}>
                        {selected.progress.completed?.length ?? 0}/{selected.path.modules.length} complete
                      </span>
                    )}
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
                          {status === 'next' ? (
                            <button
                              className="btn btn-sm btn-primary"
                              disabled={completingIdx !== null}
                              onClick={() => completeModule(selected.path.name, idx)}
                            >
                              {completingIdx === idx ? 'Marking…' : 'Mark complete'}
                            </button>
                          ) : (
                            <Badge variant={status === 'done' ? 'running' : 'stopped'}>
                              {status === 'done' ? 'Done' : status === 'locked' ? 'Locked' : 'Pending'}
                            </Badge>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>

                <div className="card-footer">
                  {!selected.progress.started ? (
                    <button
                      className="btn btn-primary"
                      disabled={starting === selected.path.name}
                      onClick={() => startPath(selected.path.name)}
                    >
                      {starting === selected.path.name ? 'Starting…' : 'Start Path'}
                    </button>
                  ) : selected.progress.nextIdx != null && selected.progress.nextIdx >= 0 ? (
                    <button
                      className="btn btn-primary"
                      disabled={completingIdx !== null}
                      onClick={() => completeModule(selected.path.name, selected.progress.nextIdx!)}
                    >
                      {completingIdx === selected.progress.nextIdx
                        ? 'Marking…'
                        : `Complete “${selected.path.modules[selected.progress.nextIdx]?.displayName || selected.path.modules[selected.progress.nextIdx]?.name || 'module'}” & continue`}
                    </button>
                  ) : (
                    <span style={{ alignSelf: 'center' }}><Badge variant="running">✓ Path complete</Badge></span>
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
