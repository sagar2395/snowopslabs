import { useState, useEffect, useCallback, useRef } from 'react'
import { api } from '../api/client'
import type { ChallengeSummary, ChallengeStatus, ChallengeRunRecord, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface ChallengesProps {
  notify: NotifyFn
  refreshTick: number
  requestConfirm: (req: ConfirmRequest) => void
}

function formatElapsed(seconds: number) {
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

function formatDate(iso: string) {
  try { return new Date(iso).toLocaleString() } catch { return iso }
}

export function Challenges({ notify, refreshTick, requestConfirm }: ChallengesProps) {
  const [challenges, setChallenges] = useState<ChallengeSummary[]>([])
  const [status, setStatus] = useState<ChallengeStatus | null>(null)
  const [history, setHistory] = useState<ChallengeRunRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [elapsed, setElapsed] = useState(0)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const load = useCallback(async () => {
    try {
      const [list, st, hist] = await Promise.all([
        api.listChallenges(),
        api.getChallengeStatus(),
        api.getChallengeHistory(),
      ])
      setChallenges(list ?? [])
      setStatus(st)
      setHistory(hist ?? [])
      setLoadError(null)
      setLoaded(true)
      // Seed elapsed from server if a challenge is active
      if (st.active && st.startedAt) {
        const secs = Math.floor((Date.now() - new Date(st.startedAt).getTime()) / 1000)
        setElapsed(Math.max(0, secs))
      }
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { load() }, [load])
  useEffect(() => { if (refreshTick > 0) load() }, [refreshTick, load])

  // Live timer when a challenge is active
  useEffect(() => {
    if (status?.active) {
      timerRef.current = setInterval(() => setElapsed(s => s + 1), 1000)
    } else {
      if (timerRef.current) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
    }
    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current)
        timerRef.current = null
      }
    }
  }, [status?.active])

  function parSeconds(parTime: string) {
    const m = parTime.match(/^(\d+)m$/)
    return m ? parseInt(m[1]) * 60 : 0
  }

  function activeChallenge() {
    if (!status?.active || !status.challenge) return null
    return challenges.find(c => c.name === status.challenge) ?? null
  }

  function scoreVariant(score: number) {
    if (score >= 80) return 'running'
    if (score >= 50) return 'category'
    return 'stopped'
  }

  if (loading) return <div className="loading" role="status">Loading challenges…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load challenges"
        message={loadError}
        onRetry={() => { setRefreshing(true); load() }}
        retrying={refreshing}
      />
    )
  }

  const active = activeChallenge()

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>Retry</button>
        </div>
      )}

      {/* Active challenge banner */}
      {status?.active && (
        <div className="card" style={{ borderColor: 'var(--accent)', marginBottom: 16 }}>
          <div className="card-header">
            <span className="card-title" style={{ color: 'var(--accent)' }}>
              Challenge in progress: {active?.displayName ?? status.challenge}
            </span>
            <span style={{ fontSize: 24, fontVariantNumeric: 'tabular-nums', fontWeight: 700, color: 'var(--accent)' }}>
              {formatElapsed(elapsed)}
              {active && parSeconds(active.parTime) > 0 && (
                <span style={{ fontSize: 13, fontWeight: 400, color: 'var(--muted)', marginLeft: 8 }}>
                  / par {active.parTime}
                </span>
              )}
            </span>
          </div>
          <div style={{ padding: '0 16px 16px', display: 'flex', gap: 10, flexWrap: 'wrap' }}>
            {status.hintsUsed != null && status.hintsUsed > 0 && (
              <Badge variant="category">{status.hintsUsed} hint{status.hintsUsed !== 1 ? 's' : ''} used</Badge>
            )}
            <div style={{ flex: 1 }} />
            <button
              className="btn btn-sm btn-danger"
              disabled={busy}
              onClick={() => requestConfirm({
                title: 'Abort challenge?',
                message: 'Aborting marks this attempt as failed with score 0. Your progress cannot be recovered.',
                confirmLabel: 'Abort',
                danger: true,
                onConfirm: async () => {
                  setBusy(true)
                  try {
                    await fetch('/api/challenges/abort', { method: 'POST' })
                    notify('info', 'Challenge aborted', '')
                    load()
                  } catch (e) {
                    notify('error', 'Abort failed', e instanceof Error ? e.message : String(e))
                  } finally {
                    setBusy(false)
                  }
                },
              })}
            >
              Abort
            </button>
            <button
              className="btn btn-sm btn-primary"
              disabled={busy}
              onClick={() => requestConfirm({
                title: 'Submit challenge?',
                message: 'Submitting runs the grading checks and records your score. Make sure you\'ve completed the task first.',
                confirmLabel: 'Submit',
                danger: false,
                onConfirm: async () => {
                  setBusy(true)
                  try {
                    const res = await fetch('/api/challenges/submit', { method: 'POST' })
                    const body = await res.json() as { score?: number; outcome?: string; error?: string }
                    if (!res.ok) throw new Error(body.error ?? `HTTP ${res.status}`)
                    notify('success', `Challenge complete — score ${body.score ?? 0}`, body.outcome ?? '')
                    load()
                  } catch (e) {
                    notify('error', 'Submit failed', e instanceof Error ? e.message : String(e))
                  } finally {
                    setBusy(false)
                  }
                },
              })}
            >
              Submit
            </button>
          </div>
        </div>
      )}

      {/* Challenge list */}
      <div className="card">
        <div className="card-header">
          <span className="card-title">Challenges ({challenges.length})</span>
          <button className="btn btn-sm" onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {challenges.length === 0 ? (
          <div className="empty-state">
            No challenges found.
            <div className="empty-hint">Challenges are discovered from <code>challenges/&lt;name&gt;/challenge.yaml</code>.</div>
          </div>
        ) : (
          challenges.map(c => {
            const isActive = status?.active && status.challenge === c.name
            const best = history
              .filter(r => r.challenge === c.name && r.outcome === 'passed')
              .reduce((m, r) => (!m || r.score > m.score) ? r : m, null as ChallengeRunRecord | null)
            return (
              <div key={c.name} className="scenario-row">
                <div className="scenario-info">
                  <div className="scenario-name">{c.displayName || c.name}</div>
                  {c.description && (
                    <div className="scenario-desc">
                      {c.description.length > 120 ? c.description.slice(0, 120) + '…' : c.description}
                    </div>
                  )}
                  <div className="scenario-tags" style={{ marginTop: 6 }}>
                    {c.category && <Badge variant="category">{c.category}</Badge>}
                    <span style={{ color: 'var(--muted)', fontSize: 12 }}>par {c.parTime}</span>
                    {best && (
                      <span style={{ color: 'var(--muted)', fontSize: 12 }}>
                        · best score: <strong style={{ color: 'var(--fg)' }}>{best.score}</strong>
                      </span>
                    )}
                  </div>
                </div>
                <Badge variant={isActive ? 'running' : best ? 'category' : 'stopped'}>
                  {isActive ? 'Active' : best ? 'Attempted' : 'Not Started'}
                </Badge>
                <div className="scenario-actions">
                  <button
                    className="btn btn-sm btn-primary"
                    disabled={!!status?.active || busy}
                    title={status?.active ? 'Finish or abort the current challenge first' : ''}
                    onClick={() => requestConfirm({
                      title: `Start "${c.displayName || c.name}"?`,
                      message: `This starts the timer. Hints cost points (–10 each). You have until you submit — going over par costs up to 20 points. Ready?`,
                      confirmLabel: 'Start challenge',
                      danger: false,
                      onConfirm: async () => {
                        setBusy(true)
                        try {
                          await fetch(`/api/challenges/${encodeURIComponent(c.name)}/start`, { method: 'POST' })
                          notify('info', `Challenge "${c.displayName || c.name}" started`, 'Timer is running. Good luck!')
                          load()
                        } catch (e) {
                          notify('error', 'Failed to start challenge', e instanceof Error ? e.message : String(e))
                        } finally {
                          setBusy(false)
                        }
                      },
                    })}
                  >
                    Start
                  </button>
                  {isActive && (
                    <button
                      className="btn btn-sm"
                      disabled={busy}
                      onClick={() => requestConfirm({
                        title: 'Use a hint?',
                        message: 'Each hint costs 10 points from your final score. Use it?',
                        confirmLabel: 'Get hint',
                        danger: false,
                        onConfirm: async () => {
                          setBusy(true)
                          try {
                            const res = await fetch('/api/challenges/hint', { method: 'POST' })
                            const body = await res.json() as { hint?: string; index?: number; total?: number; error?: string }
                            if (!res.ok) throw new Error(body.error ?? `HTTP ${res.status}`)
                            notify('info', `Hint ${body.index ?? ''}/${body.total ?? ''}`, body.hint ?? '')
                            load()
                          } catch (e) {
                            notify('error', 'Hint failed', e instanceof Error ? e.message : String(e))
                          } finally {
                            setBusy(false)
                          }
                        },
                      })}
                    >
                      Hint (–10pts)
                    </button>
                  )}
                </div>
              </div>
            )
          })
        )}
      </div>

      {/* History */}
      {history.length > 0 && (
        <div className="card" style={{ marginTop: 16 }}>
          <div className="card-header">
            <span className="card-title">Run History</span>
          </div>
          <div style={{ overflowX: 'auto' }}>
            <table className="modal-table" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th>Challenge</th>
                  <th>Date</th>
                  <th>Time</th>
                  <th>Hints</th>
                  <th>Checks</th>
                  <th>Score</th>
                  <th>Outcome</th>
                </tr>
              </thead>
              <tbody>
                {history.map((r, i) => (
                  <tr key={i}>
                    <td>{r.challenge}</td>
                    <td style={{ color: 'var(--muted)', fontSize: 12 }}>{formatDate(r.startedAt)}</td>
                    <td style={{ fontVariantNumeric: 'tabular-nums' }}>{formatElapsed(r.elapsedSeconds)}</td>
                    <td>{r.hintsUsed}</td>
                    <td>{r.checksPassed}/{r.checksTotal}</td>
                    <td><Badge variant={scoreVariant(r.score)}>{r.score}</Badge></td>
                    <td><Badge variant={r.outcome === 'passed' ? 'running' : 'stopped'}>{r.outcome}</Badge></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </>
  )
}
