import { Link } from 'react-router-dom'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { ClusterInfo, NotifyFn, PlatformProviderEntry } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

const REFRESH_SVG = (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <polyline points="23 4 23 10 17 10" />
    <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
  </svg>
)

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
  // cache key as the Platform tab, so both reflect the same cluster-derived
  // per-component state (installed ⇔ the component's namespace exists).
  const statusQ = useApiQuery(qk.status, api.getStatus, { refetchInterval: 30_000 })
  const platformQ = useApiQuery(qk.platform, api.getPlatform)
  const status = statusQ.data ?? null
  const providers = platformQ.data ?? {}
  const loading = statusQ.loading
  const loadError = statusQ.loadError
  const refreshing = statusQ.refreshing || platformQ.refreshing
  const load = () => { statusQ.reload(); platformQ.reload() }
  const { busy, run } = useJobRunner(notify)

  // ── Render guards ─────────────────────────────────────────────────────────
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

  // Prefer the live WebSocket cluster snapshot (pushed every 5 s) over the
  // last fetched status, so the card stays current without polling.
  const c = liveCluster ?? status?.cluster ?? null
  const apps = status?.apps ?? []
  const updatedAgo = lastStatusAt ? Math.max(0, Math.round((Date.now() - lastStatusAt) / 1000)) : null

  // Real installed inventory: every provider the cluster actually has, across
  // all categories (not just the single configured one). This is the same data
  // the Platform tab shows, so the Dashboard can't disagree with it.
  const installed: PlatformProviderEntry[] = Object.values(providers)
    .flat()
    .filter(e => e.installed)
    .sort((a, b) => a.category.localeCompare(b.category) || a.name.localeCompare(b.name))

  return (
    <>
      {/* Stale data hint after a failed refresh */}
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={load} disabled={refreshing}>Retry</button>
        </div>
      )}

      {/* Cluster unreachable warning */}
      {c && !c.connected && (
        <div className="banner banner-warn" role="alert">
          Cluster is unreachable — actions will fail until it is back. Try <code>labctl status</code> or switch the runtime.
        </div>
      )}

      <div className="grid">
        {/* Cluster card */}
        <div className="card">
          <div className="card-header">
            <span className="card-title">Cluster</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              {updatedAgo !== null && <span className="last-updated">updated {updatedAgo}s ago</span>}
              <button
                className={`btn-icon${refreshing ? ' spinning' : ''}`}
                onClick={load}
                disabled={refreshing}
                title="Refresh"
                aria-label="Refresh status"
              >
                {REFRESH_SVG}
              </button>
            </div>
          </div>
          {c ? (
            <div>
              <div className="row"><span className="label">Context</span><span className="value truncate" title={c.context}>{c.context || 'N/A'}</span></div>
              <div className="row"><span className="label">K8s Version</span><span className="value">{c.k8sVersion || 'N/A'}</span></div>
              <div className="row"><span className="label">Nodes</span><span className="value">{c.nodeCount}</span></div>
              <div className="row">
                <span className="label">Status</span>
                <Badge variant={c.connected ? 'running' : 'stopped'}>{c.connected ? 'Connected' : 'Disconnected'}</Badge>
              </div>
            </div>
          ) : (
            <div className="empty-state">
              No cluster detected.
              <div className="empty-hint">Activate a runtime (top right) or run <code>labctl init</code> to create one.</div>
            </div>
          )}
        </div>

        {/* Platform components card — a read-only summary of what's actually
            installed (cluster-derived). Granular install/remove lives in the
            Platform tab so there's one control surface, not two. */}
        <div className="card full-width">
          <div className="card-header">
            <span className="card-title">Platform Components</span>
            <Link to="/platform" className="btn btn-sm">Manage components →</Link>
          </div>
          {installed.length === 0 ? (
            <div className="empty-state">
              No platform components installed.
              <div className="empty-hint">
                Install the baseline below (ingress + metrics + Grafana), or add components as you need them in the <Link to="/platform">Platform tab</Link>.
              </div>
            </div>
          ) : (
            installed.map(e => (
              <div key={`${e.category}/${e.name}`} className="platform-row">
                <span className="platform-name">{categoryLabel(e.category)}</span>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <span className="value">{e.name}</span>
                  <Badge variant="running">Installed</Badge>
                </div>
              </div>
            ))
          )}
          <div className="card-footer">
            <button
              className="btn btn-primary"
              disabled={busy['platform']}
              title="Installs the slim baseline: ingress + metrics + Grafana. Add anything else in the Platform tab."
              onClick={() => run('platform', 'Install baseline', () => api.platformUp(), () => load())}
            >
              {busy['platform'] ? 'Working…' : 'Install baseline'}
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
              No applications found.
              <div className="empty-hint">Apps are discovered from <code>apps/&lt;name&gt;/app.env</code> in the project.</div>
            </div>
          ) : apps.map(a => {
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
                      Open ↗
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
      </div>
    </>
  )
}
