import { useState, useEffect, useCallback } from 'react'
import { api } from '../api/client'
import type { LeaderboardEntry, NotifyFn } from '../types'
import { ErrorState } from '../components/ErrorState'

interface LeaderboardProps {
  notify: NotifyFn
  refreshTick: number
}

function formatMttr(seconds: number) {
  if (seconds <= 0) return '—'
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

export function Leaderboard({ notify, refreshTick }: LeaderboardProps) {
  const [entries, setEntries] = useState<LeaderboardEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async () => {
    try {
      const data = await api.getLeaderboard()
      setEntries(data ?? [])
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

  if (loading) return <div className="loading" role="status">Loading leaderboard…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load leaderboard"
        message={loadError}
        onRetry={() => { setRefreshing(true); load() }}
        retrying={refreshing}
      />
    )
  }

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>Retry</button>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Leaderboard ({entries.length})</span>
          <button className="btn btn-sm" onClick={() => { setRefreshing(true); load() }} disabled={refreshing}>
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
