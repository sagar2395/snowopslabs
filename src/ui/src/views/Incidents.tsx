// SPDX-License-Identifier: Apache-2.0
//
// Incidents view — browse the fault library, inject one, work it, reveal hints
// progressively, and resolve, all from the UI. This is the operational heart of
// the "solve incidents" loop: the inject/resolve/hint endpoints are synchronous
// (not durable jobs), so we drive them directly and manage our own busy state.
//
// Silent mode is honoured: while an injected fault is unresolved and silent, its
// identity stays hidden here (the list endpoint knows the name, but showing it
// would spoil the exercise) until a detection check confirms it's resolved.
import { useState, useEffect, useRef } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { Fault, IncidentStatus, IncidentHint, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
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
  const [busy, setBusy] = useState<string | null>(null)  // action key currently running
  const [status, setStatus] = useState<IncidentStatus | null>(null)
  const [checking, setChecking] = useState(false)
  const [hints, setHints] = useState<IncidentHint[]>([])
  const [detail, setDetail] = useState<Fault | null>(null)
  const modalCloseRef = useRef<HTMLButtonElement>(null)
  const detailOpener = useRef<Element | null>(null)

  // Reset the per-incident working state whenever the active fault changes
  // (injected, resolved, or swapped) so stale hints/status can't bleed across.
  useEffect(() => {
    setHints([])
    setStatus(null)
  }, [active?.fault, active?.injectedAt])

  // Modal focus + Esc, mirroring the Scenarios view.
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
        load()  // pull fresh active state (now cleared/resolved)
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
      // "no more hints" is an expected terminal state, not an error to alarm on.
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
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={load} disabled={refreshing}>Retry</button>
        </div>
      )}

      {/* Active incident console */}
      {active ? (
        <div className="card" style={{ borderColor: 'var(--accent)', marginBottom: 16 }}>
          <div className="card-header">
            <span className="card-title" style={{ color: 'var(--accent)' }}>
              {hideIdentity ? 'Active incident — hidden (silent mode)' : `Active: ${activeFault?.displayName || active.fault}`}
            </span>
            <span style={{ color: 'var(--muted)', fontSize: 12 }}>injected {relTime(active.injectedAt)}</span>
          </div>
          <div style={{ padding: '0 16px 16px', display: 'flex', flexDirection: 'column', gap: 12 }}>
            {!hideIdentity && activeFault && (
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
                <Badge variant="category">{activeFault.category}</Badge>
                <Badge variant={SEVERITY_VARIANT[activeFault.severity] ?? 'category'}>{activeFault.severity}</Badge>
                {activeFault.description && <span style={{ color: 'var(--muted)', fontSize: 13 }}>{activeFault.description}</span>}
              </div>
            )}
            {hideIdentity && (
              <div style={{ color: 'var(--muted)', fontSize: 13 }}>
                A fault is running but its identity is hidden. Read the symptoms, form a hypothesis, then check whether you&rsquo;ve resolved it.
              </div>
            )}

            {/* Detection check result */}
            {status?.check && (
              <div className={`banner ${resolved ? 'banner-info' : 'banner-warn'}`} role="status" style={{ margin: 0 }}>
                {resolved
                  ? <>✓ Detection check <code>{status.check.name}</code> passes — resolved.</>
                  : <>✗ Not resolved yet — <code>{status.check.name}</code> fails{status.check.explanation ? `: ${status.check.explanation}` : status.check.error ? `: ${status.check.error}` : ''}.</>}
              </div>
            )}

            {/* Revealed hints */}
            {hints.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {hints.map(h => (
                  <div key={h.index} style={{ fontSize: 13, padding: '8px 12px', border: '1px solid var(--border)', borderRadius: 6, background: 'var(--surface)' }}>
                    <strong style={{ color: 'var(--muted)', fontSize: 11 }}>HINT {h.index}/{h.total}</strong>
                    <div style={{ marginTop: 2 }}>{h.text}</div>
                  </div>
                ))}
              </div>
            )}

            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
              <button className="btn btn-sm" onClick={checkStatus} disabled={checking || !!busy}>
                {checking ? 'Checking…' : 'Check if resolved'}
              </button>
              <button className="btn btn-sm" onClick={revealHint} disabled={!!busy || checking || hintsExhausted}>
                {busy === 'hint' ? 'Revealing…' : hintsExhausted ? 'No more hints' : hints.length > 0 ? 'Reveal next hint' : 'Reveal a hint'}
              </button>
              <button className="btn btn-sm btn-primary" onClick={resolve} disabled={!!busy || checking}>
                {busy === 'resolve' ? 'Resolving…' : 'Resolve'}
              </button>
              <span style={{ color: 'var(--muted)', fontSize: 12 }}>
                Hints cost score when the fault is run as a challenge.
              </span>
            </div>
          </div>
        </div>
      ) : (
        <div className="banner banner-info" role="note" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <span>No active incident. Inject one below to start diagnosing — or let the lab surprise you.</span>
          <button className="btn btn-sm" onClick={injectRandom} disabled={busy === 'inject:random'}>
            {busy === 'inject:random' ? 'Injecting…' : 'Inject random'}
          </button>
        </div>
      )}

      {/* Fault library */}
      <div className="card">
        <div className="card-header">
          <span className="card-title">Incident library ({visible.length}{q ? ` of ${faults.length}` : ''})</span>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <input
              className="runtime-select"
              type="search"
              placeholder="Filter…"
              aria-label="Filter incidents"
              value={filter}
              onChange={e => setFilter(e.target.value)}
              style={{ minWidth: 160 }}
            />
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>{refreshing ? 'Refreshing…' : 'Refresh'}</button>
          </div>
        </div>

        {visible.length === 0 ? (
          <div className="empty-state">
            {faults.length === 0
              ? <>No incidents found.<div className="empty-hint">Faults are discovered from <code>incidents/&lt;name&gt;/fault.yaml</code>.</div></>
              : <>No incidents match “{filter}”.</>}
          </div>
        ) : (
          visible.map(f => {
            const isActive = active?.fault === f.name
            return (
              <div key={f.name} className="scenario-row">
                <div className="scenario-info">
                  <div className="scenario-name">
                    {f.displayName || f.name}
                    {f.verified
                      ? <span title="Verified content" style={{ marginLeft: 6, color: 'var(--success)', fontSize: 12 }}>✓ verified</span>
                      : <span title="Unverified content" style={{ marginLeft: 6, color: 'var(--muted)', fontSize: 12 }}>unverified</span>}
                  </div>
                  {f.description && (
                    <div className="scenario-desc">
                      {f.description.length > 120 ? f.description.slice(0, 120) + '…' : f.description}
                    </div>
                  )}
                  <div className="scenario-tags" style={{ marginTop: 6 }}>
                    <Badge variant="category">{f.category}</Badge>
                    <Badge variant={SEVERITY_VARIANT[f.severity] ?? 'category'}>{f.severity}</Badge>
                  </div>
                  {requiresLabel(f) && (
                    <div style={{ color: 'var(--muted)', fontSize: 12, marginTop: 4 }}>
                      Requires: {requiresLabel(f)}
                    </div>
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
                    {busy === `inject:${f.name}` ? 'Injecting…' : 'Inject'}
                  </button>
                  <button className="btn btn-sm" onClick={() => setDetail(f)}>Details</button>
                </div>
              </div>
            )
          })
        )}
      </div>

      {/* Detail modal */}
      {detail && (
        <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) setDetail(null) }}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-label={`${detail.displayName || detail.name} details`}>
            <button ref={modalCloseRef} className="modal-close" aria-label="Close details" onClick={() => setDetail(null)}>×</button>
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
                <div style={{ color: 'var(--muted)', fontSize: 12, marginTop: 6 }}>
                  Prerequisites aren&rsquo;t installed automatically — add missing ones in the Platform tab before injecting.
                </div>
              </div>
            )}
            {detail.references && detail.references.length > 0 && (
              <div className="modal-section">
                <h3>References</h3>
                <ul style={{ margin: 0, paddingLeft: 18 }}>
                  {detail.references.map((r, i) => (
                    <li key={i}>
                      <a href={r.url} target="_blank" rel="noopener noreferrer">{r.label}</a>
                      {r.note && <span style={{ color: 'var(--muted)', fontSize: 12 }}> — {r.note}</span>}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {detail.snippets && detail.snippets.length > 0 && (
              <div className="modal-section">
                <h3>Applyable snippets</h3>
                {detail.snippets.map((s, i) => (
                  <div key={i} style={{ marginBottom: 8 }}>
                    <div style={{ fontWeight: 500 }}>{s.label}</div>
                    {s.description && <div style={{ fontSize: 12, color: 'var(--muted)' }}>{s.description}</div>}
                    {s.yaml && <pre style={{ overflowX: 'auto', fontSize: 12, background: 'var(--surface)', padding: 8, borderRadius: 6 }}><code>{s.yaml}</code></pre>}
                    {s.path && !s.yaml && <div style={{ fontSize: 12 }}><code>{s.path}</code></div>}
                  </div>
                ))}
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
