import { useState, useEffect, useRef } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { ChallengeRunRecord, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface ChallengesProps {
  notify: NotifyFn
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

// Challenges are timed, scored, interactive runs. Playing one (start/hint/submit/
// abort) is a CLI-only workflow today, so this view is intentionally read-only.
export function Challenges({ notify: _notify }: ChallengesProps) {
  const { data, loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.challenges, async () => {
    const [list, st, hist] = await Promise.all([
      api.listChallenges(),
      api.getChallengeStatus(),
      api.getChallengeHistory(),
    ])
    return { challenges: list ?? [], status: st, history: hist ?? [] }
  })
  const challenges = data?.challenges ?? []
  const status = data?.status ?? null
  const history = data?.history ?? []

  const [elapsed, setElapsed] = useState(0)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    if (status?.active && status.startedAt) {
      const secs = Math.floor((Date.now() - new Date(status.startedAt).getTime()) / 1000)
      setElapsed(Math.max(0, secs))
    }
  }, [status?.active, status?.startedAt])

  useEffect(() => {
    if (status?.active) {
      timerRef.current = setInterval(() => setElapsed(s => s + 1), 1000)
    } else if (timerRef.current) {
      clearInterval(timerRef.current)
      timerRef.current = null
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
        onRetry={load}
        retrying={refreshing}
      />
    )
  }

  const active = activeChallenge()

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

      {/* Challenges are played from the CLI; the UI is a read-only scoreboard. */}
      <div className="banner banner-info" role="note">
        <Icon name="info" size={16} className="banner-icon" />
        <span className="banner-body">
          Challenges are timed and played from the command line. Start one with{' '}
          <code>labctl challenge start &lt;name&gt;</code>, then{' '}
          <code>labctl challenge submit</code> to grade it. This view tracks progress and results.
        </span>
      </div>

      {/* Active challenge banner */}
      {status?.active && (
        <div className="card card-accent">
          <div className="card-header">
            <span className="card-title card-title-accent">
              Challenge in progress: {active?.displayName ?? status.challenge}
            </span>
            <span className="timer">
              {formatElapsed(elapsed)}
              {active && parSeconds(active.parTime) > 0 && (
                <span className="timer-par">/ par {active.parTime}</span>
              )}
            </span>
          </div>
          <div className="row-flex">
            {status.hintsUsed != null && status.hintsUsed > 0 && (
              <Badge variant="category">{status.hintsUsed} hint{status.hintsUsed !== 1 ? 's' : ''} used</Badge>
            )}
            <span className="hint-inline">
              Grade or stop from the CLI: <code>labctl challenge submit</code> ·{' '}
              <code>labctl challenge hint</code> · <code>labctl challenge abort</code>
            </span>
          </div>
        </div>
      )}

      {/* Challenge list */}
      <div className="card">
        <div className="card-header">
          <span className="card-title">Challenges ({challenges.length})</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {challenges.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="challenges" size={24} /></span>
            <div>No challenges found.</div>
            <div className="empty-hint">Challenges are discovered from <code>challenges/&lt;name&gt;/challenge.yaml</code>.</div>
          </div>
        ) : (
          <div className="card-body">
            {challenges.map(c => {
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
                    <div className="scenario-tags">
                      {c.category && <Badge variant="category">{c.category}</Badge>}
                      <span className="hint-text">par {c.parTime}</span>
                      {best && (
                        <span className="hint-text">· best score: <strong className="value">{best.score}</strong></span>
                      )}
                    </div>
                    {!isActive && (
                      <div className="cli-hint mt-2"><code>labctl challenge start {c.name}</code></div>
                    )}
                  </div>
                  <Badge variant={isActive ? 'running' : best ? 'category' : 'stopped'}>
                    {isActive ? 'Active' : best ? 'Attempted' : 'Not Started'}
                  </Badge>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* History */}
      {history.length > 0 && (
        <div className="card">
          <div className="card-header">
            <span className="card-title">Run History</span>
          </div>
          <div className="table-scroll">
            <table className="data-table">
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
                    <td className="td-muted td-nowrap">{formatDate(r.startedAt)}</td>
                    <td className="tnum">{formatElapsed(r.elapsedSeconds)}</td>
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
