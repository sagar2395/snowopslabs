import { useState, useEffect, useCallback, useRef } from 'react'
import { api } from '../api/client'
import type { Scenario, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface ScenariosProps {
  notify: NotifyFn
  refreshTick: number
  requestConfirm: (req: ConfirmRequest) => void
}

export function Scenarios({ notify, refreshTick, requestConfirm }: ScenariosProps) {
  const [scenarios, setScenarios] = useState<Scenario[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [filter, setFilter] = useState('')
  const [detail, setDetail] = useState<Scenario | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const { busy, run } = useJobRunner(notify)
  const modalCloseRef = useRef<HTMLButtonElement>(null)
  const detailOpener = useRef<Element | null>(null)

  const load = useCallback(async () => {
    try {
      const list = await api.listScenarios()
      setScenarios(list ?? [])
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

  // Modal: Esc to close, focus management
  useEffect(() => {
    if (!detail) return
    detailOpener.current = document.activeElement
    modalCloseRef.current?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') closeDetail()
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      if (detailOpener.current instanceof HTMLElement) detailOpener.current.focus()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail !== null])

  function closeDetail() { setDetail(null) }

  async function handleRefresh() {
    setRefreshing(true)
    await load()
  }

  async function openDetail(name: string) {
    setDetailLoading(true)
    setDetail({ name, displayName: name, description: '', category: '', active: false })
    try {
      const s = await api.getScenario(name)
      setDetail(s)
    } catch (e) {
      notify('error', 'Failed to load scenario details', e instanceof Error ? e.message : String(e))
      setDetail(null)
    } finally {
      setDetailLoading(false)
    }
  }

  function activate(name: string) {
    run(name, `Activate ${name}`, () => api.scenarioUp(name), () => load())
  }

  function deactivate(name: string) {
    requestConfirm({
      title: `Deactivate ${name}?`,
      message: 'This removes the scenario\'s components from the cluster. Dashboards, demo apps and experiments installed by it will be gone.',
      confirmLabel: 'Deactivate',
      onConfirm: () => run(name, `Deactivate ${name}`, () => api.scenarioDown(name), () => load()),
    })
  }

  async function copyCmd(cmd: string) {
    try {
      await navigator.clipboard.writeText(cmd)
      notify('success', 'Copied to clipboard', '')
    } catch {
      notify('error', 'Copy failed', 'Clipboard access denied — copy the command manually.')
    }
  }

  if (loading) return <div className="loading" role="status">Loading scenarios…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load scenarios"
        message={loadError}
        onRetry={handleRefresh}
        retrying={refreshing}
      />
    )
  }

  const q = filter.trim().toLowerCase()
  const visible = q
    ? scenarios.filter(s =>
        s.name.toLowerCase().includes(q) ||
        (s.displayName ?? '').toLowerCase().includes(q) ||
        (s.category ?? '').toLowerCase().includes(q) ||
        (s.description ?? '').toLowerCase().includes(q))
    : scenarios

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={handleRefresh} disabled={refreshing}>Retry</button>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Scenarios ({visible.length}{q ? ` of ${scenarios.length}` : ''})</span>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            {scenarios.length > 3 && (
              <input
                type="search"
                className="search-input"
                placeholder="Filter scenarios…"
                aria-label="Filter scenarios"
                value={filter}
                onChange={e => setFilter(e.target.value)}
              />
            )}
            <button className="btn btn-sm" onClick={handleRefresh} disabled={refreshing}>
              {refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </div>

        {visible.length === 0 ? (
          <div className="empty-state">
            {q ? <>No scenarios match “{filter}”.</> : <>No scenarios found.<div className="empty-hint">Scenarios are discovered from <code>scenarios/&lt;name&gt;/scenario.yaml</code>.</div></>}
          </div>
        ) : (
          visible.map(s => (
            <div key={s.name} className="scenario-row">
              <div className="scenario-info">
                <div className="scenario-name">{s.displayName || s.name}</div>
                {s.description && (
                  <div className="scenario-desc">
                    {s.description.length > 120 ? s.description.slice(0, 120) + '…' : s.description}
                  </div>
                )}
                <div className="scenario-tags">
                  {s.category && <Badge variant="category">{s.category}</Badge>}
                  {(s.runtimes || []).map(r => (
                    <Badge key={r} variant="runtime">{r}</Badge>
                  ))}
                </div>
              </div>
              <Badge variant={s.active ? 'running' : 'stopped'}>{s.active ? 'Active' : 'Inactive'}</Badge>
              <div className="scenario-actions">
                <button
                  className="btn btn-sm btn-primary"
                  disabled={s.active || busy[s.name]}
                  onClick={() => activate(s.name)}
                >
                  {busy[s.name] ? 'Working…' : 'Activate'}
                </button>
                <button
                  className="btn btn-sm btn-danger"
                  disabled={!s.active || busy[s.name]}
                  onClick={() => deactivate(s.name)}
                >
                  Deactivate
                </button>
                <button className="btn btn-sm" onClick={() => openDetail(s.name)}>Details</button>
              </div>
            </div>
          ))
        )}
      </div>

      {/* Detail modal */}
      {detail && (
        <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) closeDetail() }}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-label={`${detail.displayName || detail.name} details`}>
            <button ref={modalCloseRef} className="modal-close" aria-label="Close details" onClick={closeDetail}>×</button>

            {detailLoading ? (
              <div className="loading" role="status">Loading…</div>
            ) : (
              <>
                <div className="modal-header">
                  <h2>{detail.displayName || detail.name}</h2>
                  <div className="modal-meta">
                    {detail.category && <Badge variant="category">{detail.category}</Badge>}
                    <Badge variant={detail.active ? 'running' : 'stopped'}>{detail.active ? 'Active' : 'Inactive'}</Badge>
                  </div>
                </div>

                {detail.description && (
                  <div className="modal-section">
                    <h3>Description</h3>
                    <p>{detail.description}</p>
                  </div>
                )}

                {detail.prerequisites && (
                  <div className="modal-section">
                    <h3>Prerequisites</h3>
                    <div className="prereq-chips">
                      {(detail.prerequisites.platform || []).map(p => (
                        <Badge key={p} variant="category">Platform: {p}</Badge>
                      ))}
                      {(detail.prerequisites.apps || []).map(a => (
                        <Badge key={a} variant="category">App: {a}</Badge>
                      ))}
                    </div>
                  </div>
                )}

                {detail.components && detail.components.length > 0 && (
                  <div className="modal-section">
                    <h3>Components</h3>
                    <table className="modal-table">
                      <thead>
                        <tr><th>Name</th><th>Type</th><th>Namespace</th><th>Details</th></tr>
                      </thead>
                      <tbody>
                        {detail.components.map(c => (
                          <tr key={c.name}>
                            <td>{c.name}</td>
                            <td><Badge variant="category">{c.type}</Badge></td>
                            <td style={{ color: 'var(--muted)' }}>{c.namespace || 'default'}</td>
                            <td style={{ color: 'var(--muted)', fontSize: 12 }}>{c.chart || c.path || c.script || ''}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}

                {detail.explore?.urls && detail.explore.urls.length > 0 && (
                  <div className="modal-section">
                    <h3>Explore URLs</h3>
                    {detail.explore.urls.map(u => (
                      <div key={u.label} style={{ marginBottom: 6 }}>
                        <a href={u.url} target="_blank" rel="noopener noreferrer">{u.label}</a>
                        <span style={{ color: 'var(--muted)', fontSize: 12, marginLeft: 8 }}>{u.url}</span>
                      </div>
                    ))}
                  </div>
                )}

                {detail.explore?.commands && detail.explore.commands.length > 0 && (
                  <div className="modal-section">
                    <h3>Explore Commands</h3>
                    {detail.explore.commands.map(c => (
                      <div key={c.label} className="cmd-block">
                        <div className="cmd-label">{c.label}</div>
                        <code>{c.command}</code>
                        <button className="cmd-copy" onClick={() => copyCmd(c.command)}>Copy</button>
                      </div>
                    ))}
                  </div>
                )}

                {detail.explore?.tips && detail.explore.tips.length > 0 && (
                  <div className="modal-section">
                    <h3>Tips</h3>
                    <ul>{detail.explore.tips.map((t, i) => <li key={i}>{t}</li>)}</ul>
                  </div>
                )}

                <div className="card-footer">
                  <button
                    className="btn btn-primary"
                    disabled={detail.active || busy[detail.name]}
                    onClick={() => { activate(detail.name); closeDetail() }}
                  >
                    Activate
                  </button>
                  <button
                    className="btn btn-danger"
                    disabled={!detail.active || busy[detail.name]}
                    onClick={() => { closeDetail(); deactivate(detail.name) }}
                  >
                    Deactivate
                  </button>
                  <button className="btn" onClick={closeDetail}>Close</button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </>
  )
}
