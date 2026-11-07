import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { NotifyFn } from '../types'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'

interface LeaderboardProps {
  notify: NotifyFn
}

function formatMttr(seconds: number) {
  if (seconds <= 0) return '-'
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

/** The window the board ranks: every run, or only recent ones, so a game day
 *  is not buried under the lab's history. */
export type Period = 'all' | 'today' | '4h'

const PERIOD_LABELS: Record<Period, string> = {
  all: 'All time',
  today: 'Today',
  '4h': 'Last 4 hours',
}

/** The RFC 3339 start of a period, or undefined for all time. */
export function periodStart(period: Period, now: Date = new Date()): string | undefined {
  if (period === 'today') {
    const midnight = new Date(now)
    midnight.setHours(0, 0, 0, 0)
    return midnight.toISOString()
  }
  if (period === '4h') return new Date(now.getTime() - 4 * 3600 * 1000).toISOString()
  return undefined
}

export function Leaderboard(_props: LeaderboardProps) {
  const [period, setPeriod] = useState<Period>('all')
  const { data: entries = [], loading, loaded, loadError, refreshing, reload: load } = useApiQuery(
    [...qk.leaderboard, period],
    () => api.getLeaderboard(periodStart(period)),
  )

  if (loading) return <div className="loading" role="status">Loading leaderboard…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load leaderboard"
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
          <span className="banner-body">Refresh failed ({loadError}) - showing last known data.</span>
          <span className="banner-actions">
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>Retry</button>
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Leaderboard ({entries.length})</span>
          <div className="card-tools">
            <select
              className="select"
              value={period}
              aria-label="Runs to rank"
              onChange={e => setPeriod(e.target.value as Period)}
            >
              {(Object.keys(PERIOD_LABELS) as Period[]).map(p => (
                <option key={p} value={p}>{PERIOD_LABELS[p]}</option>
              ))}
            </select>
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>
              <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </div>

        {entries.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="leaderboard" size={24} /></span>
            <div>{period === 'all' ? 'No results yet - run a challenge or incident.' : `No runs in this period (${PERIOD_LABELS[period].toLowerCase()}).`}</div>
          </div>
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Rank</th>
                  <th>User</th>
                  <th>Total Score</th>
                  <th>Challenges</th>
                  <th title="Incidents fixed by hand; the escape hatch does not count">Incidents</th>
                  <th>Modules</th>
                  <th>Hints</th>
                  <th>Avg MTTR</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e, i) => (
                  <tr key={e.user}>
                    <td className="tnum">{i + 1}</td>
                    <td className="truncate" title={e.user}>{e.user}</td>
                    <td className="tnum">{e.totalScore}</td>
                    <td className="tnum">{e.challengesCompleted}</td>
                    <td className="tnum">{e.incidentsResolved}</td>
                    <td className="tnum">{e.modulesCompleted}</td>
                    <td className="tnum">{e.hintsUsed}</td>
                    <td className="tnum td-nowrap">{formatMttr(e.avgMttrSeconds)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  )
}
