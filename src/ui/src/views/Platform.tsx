import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { PlatformProviderEntry, DashboardURL, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface PlatformProps {
  notify: NotifyFn
  requestConfirm: (req: ConfirmRequest) => void
}

/** Display order for known categories; anything else sorts after, alphabetically. */
const CATEGORY_ORDER = ['ingress', 'monitoring/metrics', 'monitoring', 'logging', 'tracing', 'gitops', 'chaos', 'security', 'policy', 'secrets']

function categoryLabel(cat: string) {
  const last = cat.split('/').pop() ?? cat
  return last.charAt(0).toUpperCase() + last.slice(1)
}

function sortCategories(cats: string[]) {
  return [...cats].sort((a, b) => {
    const ia = CATEGORY_ORDER.indexOf(a)
    const ib = CATEGORY_ORDER.indexOf(b)
    if (ia !== -1 && ib !== -1) return ia - ib
    if (ia !== -1) return -1
    if (ib !== -1) return 1
    return a.localeCompare(b)
  })
}

export function Platform({ notify, requestConfirm }: PlatformProps) {
  const { data: providers = null, loading, loadError, refreshing, reload: load } = useApiQuery(qk.platform, api.getPlatform)
  // Service dashboards (Grafana, Prometheus, Traefik, ArgoCD, …). The server only
  // returns links whose namespace actually exists, so this list is exactly the
  // reachable set — which is why it lives here next to the components that serve
  // them, not on the home page where dead links used to appear.
  const dashQ = useApiQuery(qk.dashboards, () => api.getDashboards().catch(() => [] as DashboardURL[]))
  const dashboards = (dashQ.data ?? []).filter(d => d.available)
  const { busy, run } = useJobRunner(notify)

  function install(entry: PlatformProviderEntry, installedSibling?: PlatformProviderEntry) {
    const key = `${entry.category}/${entry.name}`
    const doRun = () => run(key, `Install ${entry.name}`, () => api.componentUp(entry.category, entry.name), () => load())
    if (installedSibling) {
      requestConfirm({
        title: `Swap ${categoryLabel(entry.category).toLowerCase()} provider?`,
        message: `"${installedSibling.name}" is currently installed for ${entry.category}. Installing "${entry.name}" alongside it can conflict — consider removing "${installedSibling.name}" first.`,
        confirmLabel: `Install ${entry.name} anyway`,
        onConfirm: doRun,
      })
    } else {
      doRun()
    }
  }

  function remove(entry: PlatformProviderEntry) {
    const key = `${entry.category}/${entry.name}`
    requestConfirm({
      title: `Remove ${entry.name}?`,
      message: `This uninstalls "${entry.name}" (${entry.category}) from the cluster. Anything depending on it may break.`,
      confirmLabel: 'Remove',
      onConfirm: () => run(key, `Remove ${entry.name}`, () => api.componentDown(entry.category, entry.name), () => load()),
    })
  }

  if (loading) return <div className="loading" role="status">Loading platform status…</div>

  if (loadError && !providers) {
    return (
      <ErrorState
        title="Failed to load platform status"
        message={loadError}
        onRetry={load}
        retrying={refreshing}
      />
    )
  }

  const categories = sortCategories(Object.keys(providers ?? {}).filter(c => c && (providers?.[c]?.length ?? 0) > 0))

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
          <span className="card-title">Platform Components</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {categories.length === 0 ? (
          <div className="empty-state">
            No platform providers discovered.
            <div className="empty-hint">Providers live in <code>platform/&lt;category&gt;/&lt;name&gt;/install.sh</code>.</div>
          </div>
        ) : (
          categories.map(cat => {
            const entries = providers![cat] ?? []
            const installed = entries.find(e => e.installed)
            return (
              <div key={cat} className="platform-category">
                <div className="platform-category-name">{categoryLabel(cat)}</div>
                {entries.map(entry => {
                  const key = `${entry.category}/${entry.name}`
                  // Only warn about swapping when providers are mutually exclusive
                  // (e.g. ingress, mesh). Complementary providers such as
                  // secrets/vault + secrets/external-secrets install side by side.
                  const sibling = entry.exclusive && installed && installed.name !== entry.name ? installed : undefined
                  return (
                    <div key={key} className="platform-row">
                      <div className="platform-name truncate" title={entry.name}>{entry.name}</div>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <Badge variant={entry.installed ? 'running' : 'stopped'}>
                          {entry.installed ? 'Installed' : 'Not Installed'}
                        </Badge>
                        {entry.installed ? (
                          <button
                            className="btn btn-sm btn-danger"
                            disabled={busy[key]}
                            onClick={() => remove(entry)}
                          >
                            {busy[key] ? 'Removing…' : 'Remove'}
                          </button>
                        ) : (
                          <button
                            className="btn btn-sm btn-primary"
                            disabled={busy[key]}
                            onClick={() => install(entry, sibling)}
                          >
                            {busy[key] ? 'Installing…' : sibling ? 'Swap to this' : 'Install'}
                          </button>
                        )}
                      </div>
                    </div>
                  )
                })}
              </div>
            )
          })
        )}

        <div className="card-footer">
          <button
            className="btn btn-primary"
            disabled={busy['platform-all']}
            title="Installs the slim baseline: ingress + metrics + Grafana. Add anything else per-component above."
            onClick={() => run('platform-all', 'Install baseline', () => api.platformUp(), () => load())}
          >
            {busy['platform-all'] ? 'Working…' : 'Install baseline'}
          </button>
          <button
            className="btn btn-danger"
            disabled={busy['platform-all']}
            onClick={() => requestConfirm({
              title: 'Remove the baseline platform components?',
              message: 'This uninstalls the configured baseline (ingress, metrics, Grafana). Dashboards and scenarios that rely on them will stop working. Components you added individually are removed with their own Remove button.',
              confirmLabel: 'Remove baseline',
              onConfirm: () => run('platform-all', 'Remove baseline', () => api.platformDown(), () => load()),
            })}
          >
            Remove baseline
          </button>
        </div>
      </div>

      {/* Service dashboards — moved here from the home page so the links sit
          next to the components that provide them, and only appear when the
          backing service is actually present. */}
      <div className="card" style={{ marginTop: 16 }}>
        <div className="card-header">
          <span className="card-title">Dashboards &amp; Access</span>
          <button className="btn btn-sm" onClick={() => dashQ.reload()} disabled={dashQ.refreshing}>
            {dashQ.refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
        {dashboards.length === 0 ? (
          <div className="empty-state">
            No service dashboards available yet.
            <div className="empty-hint">Install a component above (e.g. Grafana, Traefik, ArgoCD) and its dashboard link appears here.</div>
          </div>
        ) : (
          <div className="quick-links" style={{ padding: 12 }}>
            {dashboards.map(d => (
              <a key={d.name} className="quick-link" href={d.url} target="_blank" rel="noopener noreferrer">
                {d.label} ↗
              </a>
            ))}
          </div>
        )}
      </div>
    </>
  )
}
