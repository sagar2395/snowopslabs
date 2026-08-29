import { Link } from 'react-router-dom'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { ClusterInfo, NotifyFn, PlatformProviderEntry } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface DashboardProps {
  notify: NotifyFn
  liveCluster: ClusterInfo | null
  lastStatusAt: number | null
  requestConfirm: (req: ConfirmRequest) => void
}

/** Nicely label a (possibly nested) platform category key, e.g. "monitoring/metrics" → "Metrics". */
function categoryLabel(cat: string) {
  const last = cat.split('/').pop() ?? cat
  return last.charAt(0).toUpperCase() + last.slice(1)
}

export function Dashboard({ notify, liveCluster, lastStatusAt, requestConfirm }: DashboardProps) {
  // Status is polled every 30s (paused while the tab is hidden) and shared with
  // the Apps view via the same cache key. The platform inventory rides the same
  // cache key as the Platform tab, so both reflect the same cluster-derived state.
  const statusQ = useApiQuery(qk.status, api.getStatus, { refetchInterval: 30_000 })
  const platformQ = useApiQuery(qk.platform, api.getPlatform, { refetchInterval: 30_000 })
  const status = statusQ.data ?? null
  const providers = platformQ.data ?? {}
  const loading = statusQ.loading
  const loadError = statusQ.loadError
  const refreshing = statusQ.refreshing || platformQ.refreshing
  const load = () => { statusQ.reload(); platformQ.reload() }
  const { busy, run } = useJobRunner(notify)

  if (loading) {
    return <div className="loading" role="status">Loading dashboard…</div>
  }
  if (loadError && !status) {
    return (
      <ErrorState
        title="Failed to load status"
        message={loadError}
        onRetry={load}
        retrying={refreshing}
      />
    )
  }

  // Prefer the live WebSocket cluster snapshot (pushed every 5 s) over the last
  // fetched status, so the card stays current without polling.
  const c = liveCluster ?? status?.cluster ?? null
  const apps = status?.apps ?? []
  const updatedAgo = lastStatusAt ? Math.max(0, Math.round((Date.now() - lastStatusAt) / 1000)) : null

  // Real installed inventory: every provider the cluster actually has.
  const installed: PlatformProviderEntry[] = Object.values(providers)
    .flat()
    .filter(e => e.installed)
    .sort((a, b) => a.category.localeCompare(b.category) || a.name.localeCompare(b.name))

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

      {c && !c.connected && (
        <div className="banner banner-warn" role="alert">
          <Icon name="alert-triangle" size={16} className="banner-icon" />
          <span className="banner-body">
            Cluster is unreachable — actions will fail until it is back. Try <code>labctl status</code> or switch the runtime.
          </span>
        </div>
      )}

      <div className="grid">
        {/* Cluster card */}
        <div className="card">
          <div className="card-header">
            <span className="card-title">Cluster</span>
            <div className="card-tools">
              {updatedAgo !== null && <span className="last-updated">updated {updatedAgo}s ago</span>}
              <button
                className={`btn-icon${refreshing ? ' spinning' : ''}`}
                onClick={load}
                disabled={refreshing}
                title="Refresh status"
                aria-label="Refresh status"
              >
                <Icon name="refresh" size={16} />
              </button>
            </div>
          </div>
          {c ? (
            <div className="card-body">
              <div className="row"><span className="label">Context</span><span className="value truncate" title={c.context}>{c.context || 'N/A'}</span></div>
              <div className="row"><span className="label">K8s Version</span><span className="value">{c.k8sVersion || 'N/A'}</span></div>
              <div className="row"><span className="label">Nodes</span><span className="value tnum">{c.nodeCount}</span></div>
              <div className="row">
                <span className="label">Status</span>
                <Badge variant={c.connected ? 'running' : 'stopped'}>{c.connected ? 'Connected' : 'Disconnected'}</Badge>
              </div>
            </div>
          ) : (
            <div className="empty-state">
              <span className="empty-icon"><Icon name="platform" size={24} /></span>
              <div>No cluster detected.</div>
              <div className="empty-hint">Activate a runtime (bottom left) or run <code>labctl init</code> to create one.</div>
            </div>
          )}
        </div>

        {/* Platform components card — read-only summary of what's installed. */}
        <div className="card full-width">
          <div className="card-header">
            <span className="card-title">Platform Components</span>
            <Link to="/platform" className="btn btn-sm">Manage components <Icon name="arrow-right" size={14} /></Link>
          </div>
          {installed.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon"><Icon name="platform" size={24} /></span>
              <div>No platform components installed.</div>
              <div className="empty-hint">
                Install the baseline below (ingress + metrics + Grafana), or add components in the <Link to="/platform">Platform tab</Link>.
              </div>
            </div>
          ) : (
            <div className="card-body">
              {installed.map(e => (
                <div key={`${e.category}/${e.name}`} className="platform-row">
                  <span className="platform-name">{categoryLabel(e.category)}</span>
                  <div className="row-flex">
                    <span className="value">{e.name}</span>
                    <Badge variant="running"><span className="badge-dot" />Installed</Badge>
                  </div>
                </div>
              ))}
            </div>
          )}
          <div className="card-footer">
            <button
              className="btn btn-primary"
              disabled={busy['platform']}
              title="Installs the slim baseline: ingress + metrics + Grafana. Add anything else in the Platform tab."
              onClick={() => run('platform', 'Install baseline', () => api.platformUp(), () => load())}
            >
              {busy['platform'] ? 'Working…' : (<><Icon name="plus" size={15} />Install baseline</>)}
            </button>
            <Link to="/platform" className="btn">Manage all components</Link>
          </div>
        </div>

        {/* Applications card */}
        <div className="card full-width">
          <div className="card-header">
            <span className="card-title">Applications</span>
          </div>
          {apps.length === 0 ? (
            <div className="empty-state">
              <span className="empty-icon"><Icon name="apps" size={24} /></span>
              <div>No applications found.</div>
              <div className="empty-hint">Apps are discovered from <code>apps/&lt;name&gt;/app.env</code> in the project.</div>
            </div>
          ) : (
            <div className="card-body">
              {apps.map(a => {
                const bkey = `app:${a.name}`
                return (
                  <div key={a.name} className="app-row">
                    <span className="app-name truncate" title={a.name}>{a.name}</span>
                    <span className="app-meta">
                      build: {a.buildStrategy || 'N/A'} · deploy: {a.deployStrategy || 'N/A'}
                      {a.replicas && ` · replicas: ${a.replicas}`}
                      {a.ready && ` · ready: ${a.ready}`}
                    </span>
                    <Badge variant={a.deployed ? 'running' : 'stopped'}>{a.deployed ? 'Deployed' : 'Not Deployed'}</Badge>
                    <div className="btn-group">
                      {a.deployed && a.url && (
                        <a
                          className="btn btn-sm"
                          href={a.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          title={`Open ${a.url} (needs the app's ingress enabled and a hosts entry for ${a.url.replace(/^https?:\/\//, '')})`}
                        >
                          Open <Icon name="external" size={14} />
                        </a>
                      )}
                      <button
                        className="btn btn-sm btn-primary"
                        disabled={busy[bkey]}
                        onClick={() => run(bkey, `Deploy ${a.name}`, () => api.deployApp(a.name), () => load())}
                      >
                        {busy[bkey] ? 'Working…' : 'Deploy'}
                      </button>
                      <button
                        className="btn btn-sm btn-danger"
                        disabled={busy[bkey]}
                        onClick={() => requestConfirm({
                          title: `Destroy ${a.name}?`,
                          message: `This removes the "${a.name}" deployment and its resources from the cluster.`,
                          confirmLabel: 'Destroy',
                          onConfirm: () => run(bkey, `Destroy ${a.name}`, () => api.destroyApp(a.name), () => load()),
                        })}
                      >
                        Destroy
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </>
  )
}
