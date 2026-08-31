import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { LearnPathSummary, LearnPath, LearnProgress, NotifyFn } from '../types'
import type { ConfirmRequest } from '../components/ConfirmDialog'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'

interface LearnProps {
  notify: NotifyFn
  requestConfirm: (req: ConfirmRequest) => void
}

export function Learn({ notify, requestConfirm }: LearnProps) {
  const { data: paths = [], loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.learnPaths, api.listLearnPaths)
  const [selected, setSelected] = useState<{ path: LearnPath; progress: LearnProgress } | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [openingPath, setOpeningPath] = useState<string | null>(null)
  const [starting, setStarting] = useState<string | null>(null)
  const [resetting, setResetting] = useState<string | null>(null)
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

  // Reset a path's progress back to the beginning. Deliberately separate from a
  // lab reset — the cluster and your progress are independent.
  function resetPath(name: string) {
    requestConfirm({
      title: `Reset progress for "${name}"?`,
      message: 'This clears your completed modules for this path so you start from the beginning. It does not touch the cluster. This cannot be undone.',
      confirmLabel: 'Reset progress',
      onConfirm: async () => {
        setResetting(name)
        try {
          const progress = await api.resetLearnPath(name)
          notify('success', `Reset "${name}"`, 'Progress cleared — start again from the first module.')
          if (selected?.path.name === name) {
            setSelected(prev => prev ? { ...prev, progress } : null)
          }
          load()
        } catch (e) {
          notify('error', 'Failed to reset progress', e instanceof Error ? e.message : String(e))
        } finally {
          setResetting(null)
        }
      },
    })
  }

  // Completing a module is the core of the in-UI progression: the learner works
  // through a path end to end without dropping to the CLI.
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
          <Icon name="alert-triangle" size={16} className="banner-icon" />
          <span className="banner-body">Refresh failed ({loadError}) — showing last known data.</span>
          <span className="banner-actions">
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>Retry</button>
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Learning Paths ({paths.length})</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {paths.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="learn" size={24} /></span>
            <div>No learning paths found.</div>
            <div className="empty-hint">Paths are discovered from <code>learn/&lt;name&gt;/path.yaml</code>.</div>
          </div>
        ) : (
          <div className="card-body">
            {paths.map(p => {
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
                    <div className="scenario-tags">
                      {(p.tags || []).map(tag => (
                        <Badge key={tag} variant="category">{tag}</Badge>
                      ))}
                      <span className="hint-text">{p.moduleCount} modules · ~{p.estimatedMinutes} min</span>
                    </div>
                    {p.moduleCount > 0 && (
                      <div className="mt-3 maxw-sm">
                        <div className="progress">
                          <div className="progress-track">
                            <div className={`progress-fill${done ? ' is-done' : ''}`} style={{ width: `${pct}%` }} />
                          </div>
                          <span className="progress-pct">{pct}%</span>
                        </div>
                        <div className="hint-text mt-1">{p.completedCount}/{p.moduleCount} completed</div>
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
                    {p.completedCount > 0 && (
                      <button
                        className="btn btn-sm btn-danger"
                        disabled={resetting === p.name}
                        title="Clear your progress for this path and start from the beginning (does not affect the cluster)"
                        onClick={() => resetPath(p.name)}
                      >
                        {resetting === p.name ? 'Resetting…' : 'Reset progress'}
                      </button>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* Path detail modal */}
      {(detailLoading || selected) && (
        <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget && !detailLoading) setSelected(null) }}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-label="Learning path details">
            <button className="modal-close" aria-label="Close" onClick={() => setSelected(null)}><Icon name="x" size={18} /></button>

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
                      <span className="hint-text">
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
                  <div className="module-list">
                    {selected.path.modules.map((m, idx) => {
                      const status = moduleStatus(selected.progress, idx)
                      return (
                        <div
                          key={m.name}
                          className={`module-item${status === 'next' ? ' is-next' : ''}${status === 'locked' ? ' is-locked' : ''}`}
                        >
                          <span className={`module-marker${status === 'done' ? ' is-done' : ''}${status === 'next' ? ' is-next' : ''}`}>
                            {status === 'done' ? <Icon name="check" size={14} /> : status === 'next' ? <Icon name="play" size={13} /> : idx + 1}
                          </span>
                          <div className="module-info">
                            <div className="module-name">{m.displayName || m.name}</div>
                            <div className="module-sub">{m.action.type} · {m.action.ref}</div>
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
                    <Badge variant="running"><Icon name="check" size={13} /> Path complete</Badge>
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
