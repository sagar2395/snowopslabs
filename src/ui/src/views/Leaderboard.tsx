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
  if (seconds <= 0) return '—'
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

export function Leaderboard(_props: LeaderboardProps) {
  const { data: entries = [], loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.leaderboard, api.getLeaderboard)

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
          <span className="banner-body">Refresh failed ({loadError}) — showing last known data.</span>
          <span className="banner-actions">
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>Retry</button>
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Leaderboard ({entries.length})</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {entries.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="leaderboard" size={24} /></span>
            <div>No results yet — run a challenge or incident.</div>
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
                  <th>Incidents</th>
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
