import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { AppInfo, NotifyFn } from '../types'
import { DeployedBadge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface AppsProps {
  notify: NotifyFn
  requestConfirm: (req: ConfirmRequest) => void
}

export function Apps({ notify, requestConfirm }: AppsProps) {
  // /status carries apps + domainSuffix + platform in one call; the Dashboard
  // shares this same cache key, so both views fetch it once.
  const { data, loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.status, api.getStatus)
  const apps = data?.apps ?? []
  const suffix = data?.domainSuffix ?? ''
  const loggingActive = Boolean(data?.platform?.logging?.active)
  const { busy, run } = useJobRunner(notify)

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
          <span className="card-title">Applications ({apps.length})</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {apps.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="apps" size={24} /></span>
            <div>No applications found.</div>
            <div className="empty-hint">Apps are discovered from <code>apps/&lt;name&gt;/app.env</code> in the project.</div>
          </div>
        ) : (
          <div className="card-body">
            {apps.map(a => (
              <div key={a.name} className="app-row">
                <div className="app-name-col">
                  <div className="app-name truncate" title={a.name}>{a.name}</div>
                  {a.namespace && <div className="hint-text">ns: {a.namespace}</div>}
                </div>

                <div className="app-meta">
                  build: {a.buildStrategy || '—'} · deploy: {a.deployStrategy || '—'}
                  {a.replicas && ` · replicas: ${a.replicas}`}
                  {a.ready && ` · ready: ${a.ready}`}
                </div>

                <DeployedBadge deployed={a.deployed} />

                <div className="btn-group">
                  {a.deployed && a.url && (
                    <a
                      className="btn btn-sm"
                      href={a.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      title={`Open ${a.url}`}
                    >
                      Open <Icon name="external" size={14} />
                    </a>
                  )}
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
                    {busy[a.name] ? 'Working…' : (<><Icon name="hammer" size={14} />Build</>)}
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
            ))}
          </div>
        )}
      </div>
    </>
  )
}
