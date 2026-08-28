import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { NotifyFn } from '../types'
import { ErrorState } from '../components/ErrorState'

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
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={load} disabled={refreshing}>Retry</button>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Leaderboard ({entries.length})</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {entries.length === 0 ? (
          <div className="empty-state">
            No results yet — run a challenge or incident
          </div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="modal-table" style={{ width: '100%' }}>
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
                    <td style={{ fontVariantNumeric: 'tabular-nums' }}>{i + 1}</td>
                    <td style={{ maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {e.user}
                    </td>
                    <td style={{ fontVariantNumeric: 'tabular-nums' }}>{e.totalScore}</td>
                    <td style={{ fontVariantNumeric: 'tabular-nums' }}>{e.challengesCompleted}</td>
                    <td style={{ fontVariantNumeric: 'tabular-nums' }}>{e.incidentsResolved}</td>
                    <td style={{ fontVariantNumeric: 'tabular-nums' }}>{e.modulesCompleted}</td>
                    <td style={{ fontVariantNumeric: 'tabular-nums' }}>{e.hintsUsed}</td>
                    <td style={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>
                      {formatMttr(e.avgMttrSeconds)}
                    </td>
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
