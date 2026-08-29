// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { NotifyFn } from '../types'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
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
          <Icon name="alert-triangle" size={16} className="banner-icon" />
          <span className="banner-body">Refresh failed ({loadError}) — showing last known data.</span>
          <span className="banner-actions">
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>Retry</button>
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Traffic Generator</span>
        </div>

        <p className="page-intro">
          Drive synthetic load (k6) against the lab apps in-cluster, then watch the impact live in Grafana next
          to the app's own metrics. Starting replaces any active run; stopping removes the generator and its pods.
        </p>

        {profiles.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="traffic" size={24} /></span>
            <div>No traffic profiles found.</div>
            <div className="empty-hint">Profiles live in <code>services/traffic/profiles/*.js</code>.</div>
          </div>
        ) : (
          <div className="traffic-form">
            <label className="field">
              <span className="field-label">Profile</span>
              <select
                className="select"
                value={profile}
                aria-label="Traffic profile"
                onChange={e => setProfile(e.target.value)}
              >
                {profiles.map(p => <option key={p} value={p}>{p}</option>)}
              </select>
              {PROFILE_BLURB[profile] && <span className="field-help">{PROFILE_BLURB[profile]}</span>}
            </label>

            <label className="field">
              <span className="field-label">Requests / second</span>
              <input
                type="number"
                className="input maxw-input"
                min={1}
                max={10000}
                value={rps}
                aria-label="Requests per second"
                onChange={e => setRps(Number(e.target.value))}
              />
            </label>

            <label className="field">
              <span className="field-label">Duration <span className="field-optional">(optional)</span></span>
              <input
                type="text"
                className="input maxw-input"
                placeholder="profile default (e.g. 10m, 1h30m)"
                value={duration}
                aria-label="Duration"
                onChange={e => setDuration(e.target.value)}
              />
            </label>

            <div className="row-flex mt-1">
              <button
                className="btn btn-primary"
                disabled={!profile || running}
                onClick={start}
              >
                {running ? 'Working…' : (<><Icon name="play" size={15} />Start traffic</>)}
              </button>
              <button
                className="btn btn-danger"
                disabled={running}
                onClick={stop}
              >
                <Icon name="stop" size={15} />Stop traffic
              </button>
            </div>
          </div>
        )}
      </div>
    </>
  )
}
