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
  browse: 'Weighted read-mix across /, /version, /health — the shape of everyday traffic, spread over routes.',
  write:  'POSTs a JSON body (best against echo-server /echo) — exercises a write path, not just reads.',
  errors: 'Toggles go-api into simulated failure under load — watch error rate, readiness, and alerts react.',
}

// Known in-cluster targets. Multi-endpoint profiles (browse/write/errors) treat
// the chosen target as a BASE origin and append their own paths.
const TARGETS: Record<string, string> = {
  'go-api':      'http://go-api.go-api.svc.cluster.local:8080/',
  'echo-server': 'http://echo-server.echo-server.svc.cluster.local:8080/',
}

export function Traffic({ notify }: TrafficProps) {
  const { data, loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.traffic, api.getTraffic)
  const profiles = data?.profiles ?? []
  const [profile, setProfile] = useState('')
  const [rps, setRps] = useState(50)
  const [duration, setDuration] = useState('')
  const [targetApp, setTargetApp] = useState('go-api')
  const [customUrl, setCustomUrl] = useState('')
  const { busy, run } = useJobRunner(notify)

  useEffect(() => {
    if (!profile && profiles.length > 0) setProfile(profiles[0])
  }, [profile, profiles])

  // Resolve the target URL from the picker; 'custom' uses the typed URL.
  const resolvedTarget = targetApp === 'custom' ? customUrl.trim() : TARGETS[targetApp]
  const targetInvalid = targetApp === 'custom' && !/^https?:\/\//.test(customUrl.trim())

  function start() {
    run('traffic', `Traffic (${profile} → ${targetApp})`, () => api.startTraffic({
      profile,
      rps,
      duration: duration.trim() || undefined,
      target: resolvedTarget || undefined,
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
              <span className="field-label">Target</span>
              <select
                className="select"
                value={targetApp}
                aria-label="Traffic target"
                onChange={e => setTargetApp(e.target.value)}
              >
                <option value="go-api">go-api (in-cluster)</option>
                <option value="echo-server">echo-server (in-cluster)</option>
                <option value="custom">Custom URL…</option>
              </select>
              <span className="field-help">
                {targetApp === 'custom'
                  ? 'Any http(s) URL reachable from the cluster.'
                  : <>Requests go to <code>{TARGETS[targetApp]}</code></>}
              </span>
            </label>

            {targetApp === 'custom' && (
              <label className="field">
                <span className="field-label">Custom target URL</span>
                <input
                  type="text"
                  className="input maxw-input"
                  placeholder="http://my-svc.my-ns.svc.cluster.local:8080/"
                  value={customUrl}
                  aria-label="Custom target URL"
                  onChange={e => setCustomUrl(e.target.value)}
                />
                {targetInvalid && <span className="field-help field-error">Enter an http(s) URL.</span>}
              </label>
            )}

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
                disabled={!profile || running || targetInvalid}
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
