import { useState, useEffect, useRef } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { Scenario, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface ScenariosProps {
  notify: NotifyFn
  requestConfirm: (req: ConfirmRequest) => void
}

export function Scenarios({ notify, requestConfirm }: ScenariosProps) {
  const { data: scenarios = [], loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.scenarios, api.listScenarios)
  const [filter, setFilter] = useState('')
  const [detail, setDetail] = useState<Scenario | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const { busy, run } = useJobRunner(notify)
  const modalCloseRef = useRef<HTMLButtonElement>(null)
  const detailOpener = useRef<Element | null>(null)

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

  // Activating shows a requirements preview first: what the scenario will install
  // and what it expects to already be present. Prerequisites are not auto-installed.
  async function activate(name: string) {
    let s: Scenario | null = null
    try { s = await api.getScenario(name) } catch { /* fall back to a plain confirm */ }
    const plat = s?.prerequisites?.platform ?? []
    const apps = s?.prerequisites?.apps ?? []
    const comps = s?.components ?? []
    const doRun = () => run(name, `Activate ${name}`, () => api.scenarioUp(name), () => load())
    requestConfirm({
      title: `Activate ${s?.displayName || name}?`,
      danger: false,
      confirmLabel: 'Activate',
      message: (
        <div className="stack-3">
          {comps.length > 0 && (
            <div>
              <div className="field-label">This will install</div>
              <ul>
                {comps.map(c => (
                  <li key={c.name}>{c.name} <span className="hint-text">({c.type}{c.namespace ? ` → ${c.namespace}` : ''})</span></li>
                ))}
              </ul>
            </div>
          )}
          {(plat.length > 0 || apps.length > 0) && (
            <div>
              <div className="field-label">Requires (install these first if missing)</div>
              <ul>
                {plat.map(p => <li key={`p-${p}`}>Platform: <code>{p}</code></li>)}
                {apps.map(a => <li key={`a-${a}`}>App: <code>{a}</code></li>)}
              </ul>
              <div className="field-help">Prerequisites are not installed automatically — add any missing ones in the Platform tab, then activate.</div>
            </div>
          )}
          {comps.length === 0 && plat.length === 0 && apps.length === 0 && (
            <div>Activate this scenario now?</div>
          )}
        </div>
      ),
      onConfirm: doRun,
    })
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
        onRetry={load}
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
          <Icon name="alert-triangle" size={16} className="banner-icon" />
          <span className="banner-body">Refresh failed ({loadError}) — showing last known data.</span>
          <span className="banner-actions">
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>Retry</button>
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Scenarios ({visible.length}{q ? ` of ${scenarios.length}` : ''})</span>
          <div className="card-tools">
            {scenarios.length > 3 && (
              <span className="search-field">
                <Icon name="search" size={15} className="search-icon" />
                <input
                  type="search"
                  className="search-input"
                  placeholder="Filter scenarios…"
                  aria-label="Filter scenarios"
                  value={filter}
                  onChange={e => setFilter(e.target.value)}
                />
              </span>
            )}
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>
              <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </div>

        {visible.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="scenarios" size={24} /></span>
            {q ? <div>No scenarios match “{filter}”.</div> : <><div>No scenarios found.</div><div className="empty-hint">Scenarios are discovered from <code>scenarios/&lt;name&gt;/scenario.yaml</code>.</div></>}
          </div>
        ) : (
          <div className="card-body">
            {visible.map(s => (
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
                    {busy[s.name] ? 'Working…' : (<><Icon name="play" size={14} />Activate</>)}
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
            ))}
          </div>
        )}
      </div>

      {/* Detail modal */}
      {detail && (
        <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) closeDetail() }}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-label={`${detail.displayName || detail.name} details`}>
            <button ref={modalCloseRef} className="modal-close" aria-label="Close details" onClick={closeDetail}><Icon name="x" size={18} /></button>

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
                    <div className="table-scroll">
                      <table className="data-table">
                        <thead>
                          <tr><th>Name</th><th>Type</th><th>Namespace</th><th>Details</th></tr>
                        </thead>
                        <tbody>
                          {detail.components.map(c => (
                            <tr key={c.name}>
                              <td>{c.name}</td>
                              <td><Badge variant="category">{c.type}</Badge></td>
                              <td className="td-muted">{c.namespace || 'default'}</td>
                              <td className="td-muted">{c.chart || c.path || c.script || ''}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}

                {detail.explore?.urls && detail.explore.urls.length > 0 && (
                  <div className="modal-section">
                    <h3>Explore URLs</h3>
                    <div className="stack-2">
                      {detail.explore.urls.map(u => (
                        <div key={u.label} className="row-flex">
                          <a href={u.url} target="_blank" rel="noopener noreferrer">{u.label}</a>
                          <span className="hint-text">{u.url}</span>
                        </div>
                      ))}
                    </div>
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
