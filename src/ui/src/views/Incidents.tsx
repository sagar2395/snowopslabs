// SPDX-License-Identifier: Apache-2.0
//
// Incidents view — browse the fault library, inject one, work it, reveal hints
// progressively, and resolve, all from the UI. The inject/resolve/hint endpoints
// are synchronous (not durable jobs), so we drive them directly and manage our
// own busy state.
//
// Silent mode is honoured: while an injected fault is unresolved and silent, its
// identity stays hidden until a detection check confirms it's resolved.
import { useState, useEffect, useRef } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { Fault, IncidentStatus, IncidentHint, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface IncidentsProps {
  notify: NotifyFn
  requestConfirm: (req: ConfirmRequest) => void
}

const SEVERITY_VARIANT: Record<string, 'stopped' | 'category' | 'runtime'> = {
  high: 'stopped', medium: 'category', low: 'runtime',
}

/** Compact "ingress · go-api" summary of a fault's prerequisites, or "" if none. */
function requiresLabel(f: Fault) {
  const plat = f.prerequisites?.platform ?? []
  const apps = f.prerequisites?.apps ?? []
  return [...plat, ...apps].join(' · ')
}

function relTime(iso?: string) {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return ''
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s ago`
  return `${Math.floor(m / 60)}h ${m % 60}m ago`
}

export function Incidents({ notify, requestConfirm }: IncidentsProps) {
  const { data, loading, loaded, loadError, refreshing, reload: load } =
    useApiQuery(qk.incidents, api.listIncidents)
  const faults = data?.faults ?? []
  const active = data?.active ?? null

  const [filter, setFilter] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [status, setStatus] = useState<IncidentStatus | null>(null)
  const [checking, setChecking] = useState(false)
  const [hints, setHints] = useState<IncidentHint[]>([])
  const [detail, setDetail] = useState<Fault | null>(null)
  const modalCloseRef = useRef<HTMLButtonElement>(null)
  const detailOpener = useRef<Element | null>(null)

  // Reset per-incident state when the active fault changes.
  useEffect(() => {
    setHints([])
    setStatus(null)
  }, [active?.fault, active?.injectedAt])

  // Modal focus + Esc.
  useEffect(() => {
    if (!detail) return
    detailOpener.current = document.activeElement
    modalCloseRef.current?.focus()
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') setDetail(null) }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      if (detailOpener.current instanceof HTMLElement) detailOpener.current.focus()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail !== null])

  const resolved = status?.resolved === true
  const hideIdentity = !!active?.silent && !resolved

  function faultLabel(name: string) {
    const f = faults.find(x => x.name === name)
    return f?.displayName || name
  }

  async function inject(name: string, label: string) {
    if (busy) return
    setBusy(`inject:${name}`)
    try {
      const res = await api.injectIncident(name)
      notify('success', `Injected ${label}`, res.silent ? 'Silent mode — the fault is hidden until you diagnose it.' : 'Diagnose it, then resolve.')
      await load()
    } catch (e) {
      notify('error', 'Inject failed', e instanceof Error ? e.message : String(e))
    } finally { setBusy(null) }
  }

  async function injectRandom() {
    if (busy) return
    setBusy('inject:random')
    try {
      const res = await api.injectRandomIncident()
      notify('success', 'Injected a random incident', res.silent ? 'Silent mode — diagnose it from the symptoms.' : `Now solve: ${res.fault?.displayName ?? res.fault?.name ?? 'it'}.`)
      await load()
    } catch (e) {
      notify('error', 'Random inject failed', e instanceof Error ? e.message : String(e))
    } finally { setBusy(null) }
  }

  function resolve() {
    requestConfirm({
      title: 'Resolve the active incident?',
      message: 'This runs the incident\'s resolution and clears the active state. Use it when you\'ve applied the fix (or to reveal the answer).',
      confirmLabel: 'Resolve',
      onConfirm: async () => {
        if (busy) return
        setBusy('resolve')
        try {
          const res = await api.resolveIncident()
          notify('success', 'Incident resolved', `${faultLabel(res.fault)} cleared.`)
          setStatus(null)
          await load()
        } catch (e) {
          notify('error', 'Resolve failed', e instanceof Error ? e.message : String(e))
        } finally { setBusy(null) }
      },
    })
  }

  async function checkStatus() {
    setChecking(true)
    try {
      const st = await api.getIncidentStatus()
      setStatus(st)
      if (st.resolved) {
        notify('success', 'Detection check passes', 'The fault is resolved — nice work.')
        load()
      } else if (st.active) {
        notify('info', 'Still broken', st.check?.explanation || st.check?.error || 'The detection check does not pass yet.')
      }
    } catch (e) {
      notify('error', 'Status check failed', e instanceof Error ? e.message : String(e))
    } finally { setChecking(false) }
  }

  async function revealHint() {
    if (busy) return
    setBusy('hint')
    try {
      const h = await api.nextIncidentHint()
      setHints(prev => (prev.some(p => p.index === h.index) ? prev : [...prev, h]))
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      notify(/no more hints/i.test(msg) ? 'info' : 'error', 'Hint', msg)
    } finally { setBusy(null) }
  }

  if (loading) return <div className="loading" role="status">Loading incidents…</div>
  if (loadError && !loaded) {
    return <ErrorState title="Failed to load incidents" message={loadError} onRetry={load} retrying={refreshing} />
  }

  const q = filter.trim().toLowerCase()
  const visible = q
    ? faults.filter(f =>
        f.name.toLowerCase().includes(q) ||
        (f.displayName ?? '').toLowerCase().includes(q) ||
        (f.category ?? '').toLowerCase().includes(q) ||
        (f.description ?? '').toLowerCase().includes(q))
    : faults

  const activeFault = active ? faults.find(f => f.name === active.fault) ?? null : null
  const hintsExhausted = hints.length > 0 && hints[hints.length - 1].index >= hints[hints.length - 1].total

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

      {/* Active incident console */}
      {active ? (
        <div className="card card-accent">
          <div className="card-header">
            <span className="card-title card-title-accent">
              {hideIdentity ? 'Active incident — hidden (silent mode)' : `Active: ${activeFault?.displayName || active.fault}`}
            </span>
            <span className="hint-text">injected {relTime(active.injectedAt)}</span>
          </div>
          <div className="stack-3">
            {!hideIdentity && activeFault && (
              <div className="row-flex">
                <Badge variant="category">{activeFault.category}</Badge>
                <Badge variant={SEVERITY_VARIANT[activeFault.severity] ?? 'category'}>{activeFault.severity}</Badge>
                {activeFault.description && <span className="hint-inline">{activeFault.description}</span>}
              </div>
            )}
            {hideIdentity && (
              <div className="hint-inline">
                A fault is running but its identity is hidden. Read the symptoms, form a hypothesis, then check whether you&rsquo;ve resolved it.
              </div>
            )}

            {/* Detection check result */}
            {status?.check && (
              <div className={`banner check-banner ${resolved ? 'banner-success' : 'banner-warn'}`} role="status">
                <Icon name={resolved ? 'check-circle' : 'alert-triangle'} size={16} className="banner-icon" />
                <span className="banner-body">
                  {resolved
                    ? <>Detection check <code>{status.check.name}</code> passes — resolved.</>
                    : <>Not resolved yet — <code>{status.check.name}</code> fails{status.check.explanation ? `: ${status.check.explanation}` : status.check.error ? `: ${status.check.error}` : ''}.</>}
                </span>
              </div>
            )}

            {/* Revealed hints */}
            {hints.length > 0 && (
              <div className="stack-2">
                {hints.map(h => (
                  <div key={h.index} className="hint-card">
                    <span className="hint-card-label">HINT {h.index}/{h.total}</span>
                    <div className="hint-card-text">{h.text}</div>
                  </div>
                ))}
              </div>
            )}

            <div className="row-flex">
              <button className="btn btn-sm" onClick={checkStatus} disabled={checking || !!busy}>
                {checking ? 'Checking…' : (<><Icon name="target" size={14} />Check if resolved</>)}
              </button>
              <button className="btn btn-sm" onClick={revealHint} disabled={!!busy || checking || hintsExhausted}>
                <Icon name="lightbulb" size={14} />
                {busy === 'hint' ? 'Revealing…' : hintsExhausted ? 'No more hints' : hints.length > 0 ? 'Reveal next hint' : 'Reveal a hint'}
              </button>
              <button className="btn btn-sm btn-primary" onClick={resolve} disabled={!!busy || checking}>
                {busy === 'resolve' ? 'Resolving…' : 'Resolve'}
              </button>
              <span className="hint-text">Hints cost score when the fault is run as a challenge.</span>
            </div>
          </div>
        </div>
      ) : (
        <div className="banner banner-info" role="note">
          <Icon name="info" size={16} className="banner-icon" />
          <span className="banner-body">No active incident. Inject one below to start diagnosing — or let the lab surprise you.</span>
          <span className="banner-actions">
            <button className="btn btn-sm" onClick={injectRandom} disabled={busy === 'inject:random'}>
              {busy === 'inject:random' ? 'Injecting…' : (<><Icon name="zap" size={14} />Inject random</>)}
            </button>
          </span>
        </div>
      )}

      {/* Fault library */}
      <div className="card">
        <div className="card-header">
          <span className="card-title">Incident library ({visible.length}{q ? ` of ${faults.length}` : ''})</span>
          <div className="card-tools">
            <span className="search-field">
              <Icon name="search" size={15} className="search-icon" />
              <input
                className="search-input"
                type="search"
                placeholder="Filter…"
                aria-label="Filter incidents"
                value={filter}
                onChange={e => setFilter(e.target.value)}
              />
            </span>
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>
              <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </div>

        {visible.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="incidents" size={24} /></span>
            {faults.length === 0
              ? <><div>No incidents found.</div><div className="empty-hint">Faults are discovered from <code>incidents/&lt;name&gt;/fault.yaml</code>.</div></>
              : <div>No incidents match “{filter}”.</div>}
          </div>
        ) : (
          <div className="card-body">
            {visible.map(f => {
              const isActive = active?.fault === f.name
              return (
                <div key={f.name} className="scenario-row">
                  <div className="scenario-info">
                    <div className="scenario-name">
                      {f.displayName || f.name}
                      {f.verified
                        ? <span className="verified-yes" title="Verified content"><Icon name="check" size={12} /> verified</span>
                        : <span className="verified-no" title="Unverified content">unverified</span>}
                    </div>
                    {f.description && (
                      <div className="scenario-desc">
                        {f.description.length > 120 ? f.description.slice(0, 120) + '…' : f.description}
                      </div>
                    )}
                    <div className="scenario-tags">
                      <Badge variant="category">{f.category}</Badge>
                      <Badge variant={SEVERITY_VARIANT[f.severity] ?? 'category'}>{f.severity}</Badge>
                    </div>
                    {requiresLabel(f) && (
                      <div className="scenario-meta">Requires: {requiresLabel(f)}</div>
                    )}
                  </div>
                  {isActive && <Badge variant="running">Active</Badge>}
                  <div className="scenario-actions">
                    <button
                      className="btn btn-sm btn-primary"
                      disabled={!!active || busy === `inject:${f.name}`}
                      title={active ? 'Resolve the active incident first' : undefined}
                      onClick={() => inject(f.name, f.displayName || f.name)}
                    >
                      {busy === `inject:${f.name}` ? 'Injecting…' : (<><Icon name="zap" size={14} />Inject</>)}
                    </button>
                    <button className="btn btn-sm" onClick={() => setDetail(f)}>Details</button>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* Detail modal */}
      {detail && (
        <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) setDetail(null) }}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-label={`${detail.displayName || detail.name} details`}>
            <button ref={modalCloseRef} className="modal-close" aria-label="Close details" onClick={() => setDetail(null)}><Icon name="x" size={18} /></button>
            <div className="modal-header">
              <h2>{detail.displayName || detail.name}</h2>
              <div className="modal-meta">
                <Badge variant="category">{detail.category}</Badge>
                <Badge variant={SEVERITY_VARIANT[detail.severity] ?? 'category'}>{detail.severity}</Badge>
                {detail.verified ? <Badge variant="running">Verified</Badge> : <Badge variant="stopped">Unverified</Badge>}
              </div>
            </div>
            {detail.description && (
              <div className="modal-section"><p>{detail.description}</p></div>
            )}
            {((detail.prerequisites?.platform?.length ?? 0) > 0 || (detail.prerequisites?.apps?.length ?? 0) > 0) && (
              <div className="modal-section">
                <h3>Requires (install first)</h3>
                <div className="prereq-chips">
                  {(detail.prerequisites?.platform ?? []).map(p => (
                    <Badge key={`p-${p}`} variant="category">Platform: {p}</Badge>
                  ))}
                  {(detail.prerequisites?.apps ?? []).map(a => (
                    <Badge key={`a-${a}`} variant="category">App: {a}</Badge>
                  ))}
                </div>
                <div className="field-help mt-2">
                  Prerequisites aren&rsquo;t installed automatically — add missing ones in the Platform tab before injecting.
                </div>
              </div>
            )}
            {detail.references && detail.references.length > 0 && (
              <div className="modal-section">
                <h3>References</h3>
                <ul>
                  {detail.references.map((r, i) => (
                    <li key={i}>
                      <a href={r.url} target="_blank" rel="noopener noreferrer">{r.label}</a>
                      {r.note && <span className="hint-text"> — {r.note}</span>}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {detail.snippets && detail.snippets.length > 0 && (
              <div className="modal-section">
                <h3>Applyable snippets</h3>
                <div className="stack-3">
                  {detail.snippets.map((s, i) => (
                    <div key={i}>
                      <div className="field-label">{s.label}</div>
                      {s.description && <div className="field-help">{s.description}</div>}
                      {s.yaml && <pre className="modal-code"><code>{s.yaml}</code></pre>}
                      {s.path && !s.yaml && <div className="cli-hint"><code>{s.path}</code></div>}
                    </div>
                  ))}
                </div>
              </div>
            )}
            <div className="card-footer">
              <button
                className="btn btn-primary"
                disabled={!!active || busy === `inject:${detail.name}`}
                title={active ? 'Resolve the active incident first' : undefined}
                onClick={() => { const f = detail; setDetail(null); inject(f.name, f.displayName || f.name) }}
              >
                Inject this incident
              </button>
              <button className="btn" onClick={() => setDetail(null)}>Close</button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
