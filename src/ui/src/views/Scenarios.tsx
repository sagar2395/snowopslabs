import { useState, useEffect, useRef } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { Scenario, ScenarioParameter, ScenarioCheck, ScenarioVerifyResult, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface ScenariosProps {
  notify: NotifyFn
  requestConfirm: (req: ConfirmRequest) => void
}

/** A snippet/component path is stored relative to the scenario's own directory
 *  (e.g. "manifests/scaledobject.yaml"). Show it from the repo root instead —
 *  "scenarios/<name>/manifests/scaledobject.yaml" — so a learner can find the
 *  file directly. Paths already rooted at "scenarios/" are left untouched. */
function repoPath(scenarioName: string, path: string): string {
  return path.startsWith('scenarios/') ? path : `scenarios/${scenarioName}/${path}`
}

/** Components in install order regardless of format: v1 scenarios declare a flat
 *  `components` list; v2 scenarios group them under ordered `stages`. Mirrors the
 *  server's AllComponents() so the "This will install" preview and the detail
 *  table work for both. */
function allComponents(s: Pick<Scenario, 'components' | 'stages'> | null | undefined) {
  if (!s) return []
  if (s.components && s.components.length > 0) return s.components
  return (s.stages ?? []).flatMap(st => st.components ?? [])
}

export function Scenarios({ notify, requestConfirm }: ScenariosProps) {
  const { data: scenarios = [], loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.scenarios, api.listScenarios)
  const [filter, setFilter] = useState('')
  const [detail, setDetail] = useState<Scenario | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const { busy, run } = useJobRunner(notify)
  const [verifying, setVerifying] = useState<Record<string, boolean>>({})
  const [verifyResults, setVerifyResults] = useState<Record<string, ScenarioVerifyResult>>({})
  const modalCloseRef = useRef<HTMLButtonElement>(null)
  const detailOpener = useRef<Element | null>(null)
  // Parameter values for the activation being confirmed, so onConfirm reads the
  // latest edits from the dialog.
  const activateParamsRef = useRef<Record<string, string>>({})

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
    const comps = allComponents(s)
    const paramDefs = s?.parameters ?? []
    // Seed the shared ref with each parameter's default; the form mutates it in
    // place, and doRun submits whatever it holds at confirm time.
    activateParamsRef.current = Object.fromEntries(paramDefs.map(p => [p.name, p.default]))
    const doRun = () => run(name, `Activate ${name}`, () => api.scenarioUp(name, activateParamsRef.current), () => load())
    requestConfirm({
      title: `Activate ${s?.displayName || name}?`,
      danger: false,
      confirmLabel: 'Activate',
      message: (
        <div className="stack-3">
          {paramDefs.length > 0 && (
            <div>
              <div className="field-label">Parameters <span className="hint-text">(tune these to experiment — defaults reproduce the standard scenario)</span></div>
              <ParamForm parameters={paramDefs} valuesRef={activateParamsRef} />
            </div>
          )}
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

  // Verify runs the scenario's checks and shows the per-check PASS/FAIL table
  // (the CLI's `scenario verify` signal). Checks can fail while pods start, so a
  // failure is surfaced as info, not an error.
  async function verify(name: string) {
    setVerifying(v => ({ ...v, [name]: true }))
    try {
      const res = await api.scenarioVerify(name)
      setVerifyResults(r => ({ ...r, [name]: res }))
      if (res.passed) {
        notify('success', 'All checks passed', `${name} is in its expected state.`)
      } else {
        const failed = res.results.filter(c => !c.pass).length
        notify('info', `${failed} of ${res.results.length} checks failing`, 'Checks can fail while pods are still starting — re-run in a moment to let them settle.')
      }
    } catch (e) {
      notify('error', 'Verify failed', e instanceof Error ? e.message : String(e))
    } finally {
      setVerifying(v => ({ ...v, [name]: false }))
    }
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
              <div key={s.name} className="scenario-item">
                <div className="scenario-row">
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
                    <button
                      className="btn btn-sm"
                      title={s.active ? 'Run the scenario’s checks and show the pass/fail breakdown' : 'Activate the scenario before verifying'}
                      disabled={!s.active || verifying[s.name]}
                      onClick={() => verify(s.name)}
                    >
                      {verifying[s.name] ? 'Verifying…' : (<><Icon name="check-circle" size={14} />Verify</>)}
                    </button>
                    <button className="btn btn-sm" onClick={() => openDetail(s.name)}>Details</button>
                  </div>
                </div>
                {verifyResults[s.name] && <CheckResults result={verifyResults[s.name]} />}
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

                {detail.objectives && detail.objectives.length > 0 && (
                  <div className="modal-section">
                    <h3>What you'll learn</h3>
                    <ul>{detail.objectives.map((o, i) => <li key={i}>{o}</li>)}</ul>
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

                {allComponents(detail).length > 0 && (
                  <div className="modal-section">
                    <h3>Components</h3>
                    <div className="table-scroll">
                      <table className="data-table">
                        <thead>
                          <tr><th>Name</th><th>Type</th><th>Namespace</th><th>Details</th></tr>
                        </thead>
                        <tbody>
                          {allComponents(detail).map(c => (
                            <tr key={c.name}>
                              <td>{c.name}</td>
                              <td><Badge variant="category">{c.type}</Badge></td>
                              <td className="td-muted">{c.namespace || 'default'}</td>
                              <td className="td-muted">{c.chart || (c.path ? repoPath(detail.name, c.path) : c.script) || ''}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}

                {detail.snippets && detail.snippets.length > 0 && (
                  <div className="modal-section">
                    <h3>How it's implemented</h3>
                    <div className="stack-3">
                      {detail.snippets.map(sn => (
                        <div key={sn.label} className="snippet">
                          <div className="snippet-head">
                            <span className="snippet-label">{sn.label}</span>
                            {sn.yaml && <button className="cmd-copy" onClick={() => copyCmd(sn.yaml!)}>Copy</button>}
                          </div>
                          {sn.description && <div className="snippet-desc">{sn.description}</div>}
                          {sn.path && <div className="hint-text snippet-source">source: <code>{repoPath(detail.name, sn.path)}</code></div>}
                          {sn.yaml && <pre className="snippet-code">{sn.yaml}</pre>}
                        </div>
                      ))}
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

                {detail.checks && detail.checks.length > 0 && (
                  <div className="modal-section">
                    <h3>What "success" checks <span className="hint-text">(run these with Verify)</span></h3>
                    <div className="table-scroll">
                      <table className="data-table">
                        <thead>
                          <tr><th>Check</th><th>Type</th><th>Asserts</th></tr>
                        </thead>
                        <tbody>
                          {detail.checks.map(c => (
                            <tr key={c.name}>
                              <td>{c.name}</td>
                              <td><Badge variant="category">{c.type || 'check'}</Badge></td>
                              <td className="td-muted"><code>{checkAssertion(c)}</code></td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}

                {verifyResults[detail.name] && (
                  <div className="modal-section">
                    <h3>Verification</h3>
                    <CheckResults result={verifyResults[detail.name]} />
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
                  <button
                    className="btn"
                    disabled={!detail.active || verifying[detail.name]}
                    title={detail.active ? 'Run the scenario’s checks' : 'Activate the scenario before verifying'}
                    onClick={() => verify(detail.name)}
                  >
                    {verifying[detail.name] ? 'Verifying…' : 'Verify'}
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

/** Renders a check's assertion as a compact expression, e.g.
 *  "deployment/keda-operator {.status.readyReplicas} >= 1". */
function checkAssertion(c: ScenarioCheck): string {
  const subject = c.type === 'promql' ? c.query : [c.resource, c.jsonpath].filter(Boolean).join(' ')
  return [subject, c.operator, c.value].filter(Boolean).join(' ')
}

/** Renders an editable field per scenario parameter inside the Activate dialog.
 *  Values are mirrored into valuesRef.current so the dialog's confirm closure
 *  submits the latest edits. Integer params render as bounded number inputs. */
function ParamForm({ parameters, valuesRef }: { parameters: ScenarioParameter[]; valuesRef: React.MutableRefObject<Record<string, string>> }) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(parameters.map(p => [p.name, p.default])),
  )
  function update(name: string, v: string) {
    valuesRef.current = { ...valuesRef.current, [name]: v }
    setValues(prev => ({ ...prev, [name]: v }))
  }
  return (
    <div className="param-form">
      {parameters.map(p => {
        const id = `param-${p.name}`
        const isInt = p.type === 'int'
        return (
          <div key={p.name} className="param-field">
            <label htmlFor={id}>
              {p.displayName || p.name}
              {isInt && (p.min !== undefined || p.max !== undefined) && (
                <span className="hint-text"> ({p.min ?? '−∞'}–{p.max ?? '∞'})</span>
              )}
            </label>
            <input
              id={id}
              type={isInt ? 'number' : 'text'}
              className="param-input"
              value={values[p.name] ?? ''}
              min={isInt ? p.min : undefined}
              max={isInt ? p.max : undefined}
              onChange={e => update(p.name, e.target.value)}
            />
            {p.description && <div className="field-help">{p.description}</div>}
          </div>
        )
      })}
    </div>
  )
}

/** Renders the per-check PASS/FAIL breakdown from a scenario verify run, plus a
 *  summary banner. Mirrors the CLI's `scenario verify` table so a browser-only
 *  user gets the same signal: which checks hold, and why a failing one failed. */
function CheckResults({ result }: { result: ScenarioVerifyResult }) {
  const passed = result.results.filter(c => c.pass).length
  const total = result.results.length
  return (
    <div className="verify-results" role="status" aria-live="polite">
      <div className={`banner ${result.passed ? 'banner-success' : 'banner-warn'}`}>
        <Icon name={result.passed ? 'check-circle' : 'alert-triangle'} size={16} className="banner-icon" />
        <span className="banner-body">
          {result.passed
            ? <>All {total} checks passed — the scenario is in its expected state.</>
            : <>{passed} of {total} checks passing. Checks can fail while pods are still starting — re-run in a moment.</>}
        </span>
      </div>
      <div className="table-scroll">
        <table className="data-table">
          <thead>
            <tr><th>Check</th><th>Result</th><th>Got</th><th>Want</th></tr>
          </thead>
          <tbody>
            {result.results.map(c => (
              <tr key={c.name}>
                <td>{c.name}{c.type ? <span className="hint-text"> ({c.type})</span> : null}</td>
                <td><Badge variant={c.pass ? 'running' : 'stopped'}>{c.pass ? 'PASS' : 'FAIL'}</Badge></td>
                <td className="td-muted">{c.error ? <span className="verify-error">{c.error}</span> : (c.got || '—')}</td>
                <td className="td-muted">{c.want || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
