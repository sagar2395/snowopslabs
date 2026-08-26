import { useState, useEffect, useCallback } from 'react'
import { api } from '../api/client'
import type { AppInfo, StatusResponse, NotifyFn } from '../types'
import { DeployedBadge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface AppsProps {
  notify: NotifyFn
  refreshTick: number
  requestConfirm: (req: ConfirmRequest) => void
}

export function Apps({ notify, refreshTick, requestConfirm }: AppsProps) {
  const [apps, setApps] = useState<AppInfo[]>([])
  const [suffix, setSuffix] = useState('')
  const [loggingActive, setLoggingActive] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const { busy, run } = useJobRunner(notify)

  const load = useCallback(async () => {
    try {
      // Use /status to get apps + domainSuffix + platform in one call
      const s: StatusResponse = await api.getStatus()
      setApps(s.apps ?? [])
      if (s.domainSuffix) setSuffix(s.domainSuffix)
      setLoggingActive(Boolean(s.platform?.logging?.active))
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

  async function handleRefresh() {
    setRefreshing(true)
    await load()
  }

  function openLogs(a: AppInfo) {
    if (!suffix) {
      notify('error', 'Domain suffix not loaded', 'Refresh and try again.')
      return
    }
    if (!loggingActive) {
      notify('info', 'Logging is not installed', 'Install the logging component (Loki) from the Platform tab to view logs in Grafana.')
      return
    }
    const ns = a.namespace || a.name
    const query = `{namespace="${ns}"}`
    const explore = JSON.stringify({ datasource: 'Loki', queries: [{ refId: 'A', expr: query }] })
    window.open(`http://grafana.${suffix}/explore?orgId=1&left=${encodeURIComponent(explore)}`, '_blank', 'noopener')
  }

  if (loading) return <div className="loading" role="status">Loading apps…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load apps"
        message={loadError}
        onRetry={handleRefresh}
        retrying={refreshing}
      />
    )
  }

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={handleRefresh} disabled={refreshing}>Retry</button>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Applications ({apps.length})</span>
          <button className="btn btn-sm" onClick={handleRefresh} disabled={refreshing}>
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {apps.length === 0 ? (
          <div className="empty-state">
            No applications found.
            <div className="empty-hint">Apps are discovered from <code>apps/&lt;name&gt;/app.env</code> in the project.</div>
          </div>
        ) : (
          apps.map(a => (
            <div key={a.name} className="app-row">
              <div style={{ flex: 1, minWidth: 120 }}>
                <div className="app-name truncate" title={a.name}>{a.name}</div>
                {a.namespace && (
                  <div style={{ color: 'var(--muted)', fontSize: 12 }}>ns: {a.namespace}</div>
                )}
              </div>

              <div className="app-meta">
                <span>build: {a.buildStrategy || '—'}</span>
                <span style={{ margin: '0 6px' }}>·</span>
                <span>deploy: {a.deployStrategy || '—'}</span>
                {a.replicas && <><span style={{ margin: '0 6px' }}>·</span><span>replicas: {a.replicas}</span></>}
                {a.ready    && <><span style={{ margin: '0 6px' }}>·</span><span>ready: {a.ready}</span></>}
              </div>

              <DeployedBadge deployed={a.deployed} />

              <div className="btn-group">
                <button
                  className="btn btn-sm"
                  onClick={() => openLogs(a)}
                  title={loggingActive ? 'Open logs in Grafana' : 'Requires the logging component (Loki)'}
                >
                  Logs
                </button>
                <button
                  className="btn btn-sm"
                  disabled={busy[a.name]}
                  onClick={() => run(a.name, `Build ${a.name}`, () => api.buildApp(a.name), () => load())}
                >
                  {busy[a.name] ? 'Working…' : 'Build'}
                </button>
                <button
                  className="btn btn-sm btn-primary"
                  disabled={busy[a.name]}
                  onClick={() => run(a.name, `Deploy ${a.name}`, () => api.deployApp(a.name), () => load())}
                >
                  Deploy
                </button>
                <button
                  className="btn btn-sm btn-danger"
                  disabled={busy[a.name]}
                  onClick={() => requestConfirm({
                    title: `Destroy ${a.name}?`,
                    message: `This removes the "${a.name}" deployment and its resources from the cluster.`,
                    confirmLabel: 'Destroy',
                    onConfirm: () => run(a.name, `Destroy ${a.name}`, () => api.destroyApp(a.name), () => load()),
                  })}
                >
                  Destroy
                </button>
              </div>
            </div>
          ))
        )}
      </div>
    </>
  )
}
