import { useState, useEffect, useCallback } from 'react'
import { api } from '../api/client'
import type { ClusterInfo, StatusResponse, DashboardURL, NotifyFn } from '../types'
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
  refreshTick: number
  liveCluster: ClusterInfo | null
  lastStatusAt: number | null
  requestConfirm: (req: ConfirmRequest) => void
}

const PLATFORM_CATEGORIES = ['ingress', 'metrics', 'logging', 'tracing', 'gitops', 'chaos', 'policy', 'secrets']

export function Dashboard({ notify, refreshTick, liveCluster, lastStatusAt, requestConfirm }: DashboardProps) {
  const [status, setStatus] = useState<StatusResponse | null>(null)
  const [dashboards, setDashboards] = useState<DashboardURL[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const { busy, run } = useJobRunner(notify)

  const load = useCallback(async () => {
    try {
      const [s, d] = await Promise.all([
        api.getStatus(),
        api.getDashboards().catch(() => [] as DashboardURL[]),
      ])
      setStatus(s)
      setDashboards(d)
      setLoadError(null)
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  // Refresh when actions complete / WS reconnects
  useEffect(() => { if (refreshTick > 0) load() }, [refreshTick, load])

  // Periodic fallback refresh — skipped while the browser tab is hidden.
  useEffect(() => {
    const t = setInterval(() => { if (!document.hidden) load() }, 30_000)
    return () => clearInterval(t)
  }, [load])

  async function handleRefresh() {
    setRefreshing(true)
    await load()
  }

  // ── Render guards ─────────────────────────────────────────────────────────
  if (loading) {
    return <div className="loading" role="status">Loading dashboard…</div>
  }
  if (loadError && !status) {
    return (
      <ErrorState
        title="Failed to load status"
        message={loadError}
        onRetry={handleRefresh}
        retrying={refreshing}
      />
    )
  }

  // Prefer the live WebSocket cluster snapshot (pushed every 5 s) over the
  // last fetched status, so the card stays current without polling.
  const c = liveCluster ?? status?.cluster ?? null
  const p = status?.platform ?? {}
  const apps = status?.apps ?? []
  const links = dashboards.filter(d => d.available)
  const updatedAgo = lastStatusAt ? Math.max(0, Math.round((Date.now() - lastStatusAt) / 1000)) : null

  return (
    <>
      {/* Stale data hint after a failed refresh */}
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={handleRefresh} disabled={refreshing}>Retry</button>
        </div>
      )}

      {/* Cluster unreachable warning */}
      {c && !c.connected && (
        <div className="banner banner-warn" role="alert">
          Cluster is unreachable — actions will fail until it is back. Try <code>labctl status</code> or switch the runtime.
        </div>
      )}

      {/* Quick links */}
      {links.length > 0 && (
        <div className="quick-links">
          {links.map(d => (
            <a key={d.name} className="quick-link" href={d.url} target="_blank" rel="noopener noreferrer">
              {d.label} ↗
            </a>
          ))}
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
                onClick={handleRefresh}
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

        {/* Platform components card */}
        <div className="card full-width">
          <div className="card-header">
            <span className="card-title">Platform Components</span>
          </div>
          {PLATFORM_CATEGORIES
            .filter(key => p[key])
            .map(key => {
              const comp = p[key]!
              const bkey = `comp:${key}`
              return (
                <div key={key} className="platform-row">
                  <span className="platform-name">{key}</span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                    <span className="value">{comp.provider || 'none'}</span>
                    <Badge variant={comp.active ? 'running' : 'stopped'}>{comp.active ? 'Running' : 'Stopped'}</Badge>
                    {comp.provider && (
                      comp.active
                        ? (
                          <button
                            className="btn btn-sm btn-danger"
                            disabled={busy[bkey]}
                            onClick={() => requestConfirm({
                              title: `Remove ${comp.provider}?`,
                              message: `This uninstalls the ${key} component "${comp.provider}" from the cluster. Workloads that depend on it may break.`,
                              confirmLabel: 'Remove',
                              onConfirm: () => run(bkey, `Remove ${comp.provider}`, () => api.componentDown(key, comp.provider), () => load()),
                            })}
                          >
                            {busy[bkey] ? 'Removing…' : 'Remove'}
                          </button>
                        ) : (
                          <button
                            className="btn btn-sm btn-primary"
                            disabled={busy[bkey]}
                            onClick={() => run(bkey, `Install ${comp.provider}`, () => api.componentUp(key, comp.provider), () => load())}
                          >
                            {busy[bkey] ? 'Installing…' : 'Install'}
                          </button>
                        )
                    )}
                  </div>
                </div>
              )
            })}
          {Object.keys(p).length === 0 && (
            <div className="empty-state">
              No platform data.
              <div className="empty-hint">Check that providers are configured in <code>.env</code> (INGRESS_PROVIDER, METRICS_PROVIDER, …).</div>
            </div>
          )}
          <div className="card-footer">
            <button
              className="btn btn-primary"
              disabled={busy['platform']}
              onClick={() => run('platform', 'Platform install', () => api.platformUp(), () => load())}
            >
              {busy['platform'] ? 'Working…' : 'Install Platform'}
            </button>
            <button
              className="btn btn-danger"
              disabled={busy['platform']}
              onClick={() => requestConfirm({
                title: 'Remove the whole platform?',
                message: 'This uninstalls ingress, metrics, Grafana, logging and tracing from the cluster. Dashboards and scenario components will stop working.',
                confirmLabel: 'Remove platform',
                onConfirm: () => run('platform', 'Platform removal', () => api.platformDown(), () => load()),
              })}
            >
              Remove Platform
            </button>
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
