import { useState, useEffect, useCallback } from 'react'
import { api } from '../api/client'
import type { PlatformProvidersMap, PlatformProviderEntry, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface PlatformProps {
  notify: NotifyFn
  refreshTick: number
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

export function Platform({ notify, refreshTick, requestConfirm }: PlatformProps) {
  const [providers, setProviders] = useState<PlatformProvidersMap | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const { busy, run } = useJobRunner(notify)

  const load = useCallback(async () => {
    try {
      const p = await api.getPlatform()
      setProviders(p)
      setLoadError(null)
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
        onRetry={handleRefresh}
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
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={handleRefresh} disabled={refreshing}>Retry</button>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Platform Components</span>
          <button className="btn btn-sm" onClick={handleRefresh} disabled={refreshing}>
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
                  const sibling = installed && installed.name !== entry.name ? installed : undefined
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
            onClick={() => run('platform-all', 'Platform install', () => api.platformUp(), () => load())}
          >
            {busy['platform-all'] ? 'Working…' : 'Install All'}
          </button>
          <button
            className="btn btn-danger"
            disabled={busy['platform-all']}
            onClick={() => requestConfirm({
              title: 'Remove all platform components?',
              message: 'This uninstalls every configured platform component (ingress, metrics, Grafana, logging, tracing). Dashboards and scenarios will stop working.',
              confirmLabel: 'Remove all',
              onConfirm: () => run('platform-all', 'Platform removal', () => api.platformDown(), () => load()),
            })}
          >
            Remove All
          </button>
        </div>
      </div>
    </>
  )
}
