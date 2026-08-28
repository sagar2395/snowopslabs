// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { NotifyFn } from '../types'
import { ErrorState } from '../components/ErrorState'
import { useJobRunner } from '../hooks/useJobRunner'

interface TrafficProps {
  notify: NotifyFn
}

// Short blurbs so the operator knows what each k6 profile does before running it.
const PROFILE_BLURB: Record<string, string> = {
  steady: 'Constant request rate for the duration — a stable baseline to watch autoscaling and latency settle.',
  spike:  'Baseline, then a sharp ~10× spike and recovery (~6 min) — see how the stack absorbs and sheds a burst.',
  soak:   'Sustained moderate load over a long window (default 2h) — surface slow leaks and gradual degradation.',
}

export function Traffic({ notify }: TrafficProps) {
  const { data, loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.traffic, api.getTraffic)
  const profiles = data?.profiles ?? []
  const [profile, setProfile] = useState('')
  const [rps, setRps] = useState(50)
  const [duration, setDuration] = useState('')
  const { busy, run } = useJobRunner(notify)

  // Default the selection to the first profile once they load.
  useEffect(() => {
    if (!profile && profiles.length > 0) setProfile(profiles[0])
  }, [profile, profiles])

  function start() {
    run('traffic', `Traffic (${profile})`, () => api.startTraffic({
      profile,
      rps,
      duration: duration.trim() || undefined,
    }))
  }
  function stop() {
    run('traffic', 'Stop traffic', () => api.stopTraffic())
  }

  if (loading) return <div className="loading" role="status">Loading traffic profiles…</div>

  if (loadError && !loaded) {
    return <ErrorState title="Failed to load traffic profiles" message={loadError} onRetry={load} retrying={refreshing} />
  }

  const running = Boolean(busy['traffic'])

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
          <span className="card-title">Traffic Generator</span>
        </div>

        <p style={{ color: 'var(--muted)', fontSize: 'var(--text-sm)', margin: '0 0 18px', lineHeight: 1.6 }}>
          Drive synthetic load (k6) against the lab apps in-cluster, then watch the impact live in Grafana next
          to the app's own metrics. Starting replaces any active run; stopping removes the generator and its pods.
        </p>

        {profiles.length === 0 ? (
          <div className="empty-state">
            No traffic profiles found.
            <div className="empty-hint">Profiles live in <code>services/traffic/profiles/*.js</code>.</div>
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 520 }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ fontSize: 'var(--text-sm)', fontWeight: 600 }}>Profile</span>
              <select
                className="runtime-select"
                value={profile}
                aria-label="Traffic profile"
                onChange={e => setProfile(e.target.value)}
              >
                {profiles.map(p => <option key={p} value={p}>{p}</option>)}
              </select>
              {PROFILE_BLURB[profile] && (
                <span style={{ fontSize: 'var(--text-xs)', color: 'var(--muted)', lineHeight: 1.5 }}>
                  {PROFILE_BLURB[profile]}
                </span>
              )}
            </label>

            <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ fontSize: 'var(--text-sm)', fontWeight: 600 }}>Requests / second</span>
              <input
                type="number"
                className="search-input"
                min={1}
                max={10000}
                value={rps}
                aria-label="Requests per second"
                onChange={e => setRps(Number(e.target.value))}
                style={{ maxWidth: 160 }}
              />
            </label>

            <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ fontSize: 'var(--text-sm)', fontWeight: 600 }}>Duration <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(optional)</span></span>
              <input
                type="text"
                className="search-input"
                placeholder="profile default (e.g. 10m, 1h30m)"
                value={duration}
                aria-label="Duration"
                onChange={e => setDuration(e.target.value)}
                style={{ maxWidth: 260 }}
              />
            </label>

            <div style={{ display: 'flex', gap: 10, marginTop: 4 }}>
              <button
                className="btn btn-primary"
                disabled={!profile || running}
                onClick={start}
              >
                {running ? 'Working…' : 'Start traffic'}
              </button>
              <button
                className="btn btn-danger"
                disabled={running}
                onClick={stop}
              >
                Stop traffic
              </button>
            </div>
          </div>
        )}
      </div>
    </>
  )
}
