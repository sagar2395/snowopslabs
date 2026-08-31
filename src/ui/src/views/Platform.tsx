import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { PlatformProviderEntry, PlatformComponentDetail, DashboardURL, NotifyFn } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
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
  // Poll while this tab is open (paused when the tab is hidden) so per-component
  // changes made out of band — a namespace deleted from the CLI, a cluster reset —
  // are reflected without a manual refresh. `installed` is re-derived from the
  // live cluster on every request server-side, so a poll always shows the truth.
  const { data: providers = null, loading, loadError, refreshing, reload: load } =
    useApiQuery(qk.platform, api.getPlatform, { refetchInterval: 15_000 })
  // Service dashboards (Grafana, Prometheus, Traefik, ArgoCD, …). The server only
  // returns links whose namespace actually exists, so this list is exactly the
  // reachable set — which is why it lives here next to the components that serve them.
  const dashQ = useApiQuery(qk.dashboards, () => api.getDashboards().catch(() => [] as DashboardURL[]), { refetchInterval: 15_000 })
  const dashboards = (dashQ.data ?? []).filter(d => d.available)
  const { busy, run } = useJobRunner(notify)
  const [detail, setDetail] = useState<PlatformComponentDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  async function openDetail(entry: PlatformProviderEntry) {
    setDetailLoading(true)
    setDetail({ category: entry.category, name: entry.name, namespace: '', installed: entry.installed, description: '', provides: [], ports: [], dependencies: [], resources: [], chart: '', installCommands: [], usedInScenarios: [] })
    try {
      setDetail(await api.getComponent(entry.category, entry.name))
    } catch (e) {
      notify('error', 'Failed to load component details', e instanceof Error ? e.message : String(e))
      setDetail(null)
    } finally {
      setDetailLoading(false)
    }
  }

  async function copyCmd(cmd: string) {
    try {
      await navigator.clipboard.writeText(cmd)
      notify('success', 'Copied to clipboard', '')
    } catch {
      notify('error', 'Copy failed', 'Clipboard access denied — copy the command manually.')
    }
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
          <Icon name="alert-triangle" size={16} className="banner-icon" />
          <span className="banner-body">Refresh failed ({loadError}) — showing last known data.</span>
          <span className="banner-actions">
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>Retry</button>
          </span>
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">Platform Components</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {categories.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="platform" size={24} /></span>
            <div>No platform providers discovered.</div>
            <div className="empty-hint">Providers live in <code>platform/&lt;category&gt;/&lt;name&gt;/install.sh</code>.</div>
          </div>
        ) : (
          <div className="card-body">
            {categories.map(cat => {
              const entries = providers![cat] ?? []
              const installed = entries.find(e => e.installed)
              return (
                <div key={cat} className="platform-category">
                  <div className="platform-category-name">{categoryLabel(cat)}</div>
                  {entries.map(entry => {
                    const key = `${entry.category}/${entry.name}`
                    // Only warn about swapping mutually-exclusive providers.
                    const sibling = entry.exclusive && installed && installed.name !== entry.name ? installed : undefined
                    return (
                      <div key={key} className="platform-row">
                        <div className="platform-name truncate" title={entry.name}>{entry.name}</div>
                        <div className="row-flex">
                          <Badge variant={entry.installed ? 'running' : 'stopped'}>
                            {entry.installed ? 'Installed' : 'Not Installed'}
                          </Badge>
                          <button className="btn btn-sm" onClick={() => openDetail(entry)}>Details</button>
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
            })}
          </div>
        )}

        <div className="card-footer">
          <button
            className="btn btn-primary"
            disabled={busy['platform-all']}
            title="Installs the slim baseline: ingress + metrics + Grafana. Add anything else per-component above."
            onClick={() => run('platform-all', 'Install baseline', () => api.platformUp(), () => load())}
          >
            {busy['platform-all'] ? 'Working…' : (<><Icon name="plus" size={15} />Install baseline</>)}
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
          <button
            className="btn btn-danger platform-reset"
            disabled={busy['lab-reset']}
            title="Tear the lab back to its initial state: deactivate all scenarios and incidents, destroy deployed apps, and uninstall non-baseline platform components. The cluster itself stays up."
            onClick={() => requestConfirm({
              title: 'Reset the whole lab?',
              message: 'This deactivates every scenario and incident, destroys all deployed apps, and uninstalls every platform component except the ingress baseline. The cluster stays up, and your learning progress is kept. Scenarios and incidents can be re-activated afterwards. This cannot be undone — consider taking a snapshot first.',
              confirmLabel: 'Reset lab',
              onConfirm: () => run('lab-reset', 'Reset lab', () => api.resetLab(), () => { load(); dashQ.reload() }),
            })}
          >
            {busy['lab-reset'] ? 'Resetting…' : (<><Icon name="refresh" size={15} />Reset lab</>)}
          </button>
        </div>
      </div>

      {/* Service dashboards — only appear when the backing service is present. */}
      <div className="card">
        <div className="card-header">
          <span className="card-title">Dashboards &amp; Access</span>
          <button className="btn btn-sm" onClick={() => dashQ.reload()} disabled={dashQ.refreshing}>
            <Icon name="refresh" size={14} />{dashQ.refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
        {dashboards.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="external" size={24} /></span>
            <div>No service dashboards available yet.</div>
            <div className="empty-hint">Install a component above (e.g. Grafana, Traefik, ArgoCD) and its dashboard link appears here.</div>
          </div>
        ) : (
          <>
            <div className="quick-links">
              {dashboards.map(d => (
                <a key={d.name} className="quick-link" href={d.url} target="_blank" rel="noopener noreferrer">
                  {d.label} <Icon name="external" size={14} />
                </a>
              ))}
            </div>
            {dashboards.some(d => d.name === 'grafana') && (
              <details className="grafana-guide">
                <summary>Configuring Grafana</summary>
                <div className="field-help">
                  <p><strong>Log in:</strong> <code>admin</code> / <code>admin</code> (override with the <code>GRAFANA_ADMIN_PASSWORD</code> env var before installing).</p>
                  <p><strong>Data sources</strong> are auto-provisioned — you don't create them by hand. Three come pre-wired, each with a fixed UID you'll see referenced in panels:</p>
                  <ul>
                    <li><code>Prometheus</code> (uid <code>prometheus</code>) — metrics, the default source</li>
                    <li><code>Loki</code> (uid <code>loki</code>) — logs</li>
                    <li><code>Tempo</code> (uid <code>tempo</code>) — traces</li>
                  </ul>
                  <p><strong>Dashboards</strong> are auto-provisioned too: the baseline set ships with Grafana, and each scenario adds its own when you activate it. Find them under <em>Dashboards</em> in Grafana — no import needed.</p>
                  <p><strong>Reading a panel:</strong> if a query shows a <code>$</code> variable (e.g. <code>$__rate_interval</code>), that's Grafana's own interval macro and resolves automatically. The install commands on each component's <em>Details</em> page already have their <code>$NAMESPACE</code> / <code>{'${DOMAIN_SUFFIX}'}</code> filled in with this lab's values.</p>
                </div>
              </details>
            )}
          </>
        )}
      </div>

      {detail && (
        <ComponentDetailModal
          detail={detail}
          loading={detailLoading}
          onClose={() => setDetail(null)}
          onCopy={copyCmd}
        />
      )}
    </>
  )
}

/** Per-tool details: what it is, what it provides/needs, the exact helm/kubectl
 *  commands install.sh runs, and which scenarios depend on it. */
function ComponentDetailModal({ detail, loading, onClose, onCopy }: {
  detail: PlatformComponentDetail
  loading: boolean
  onClose: () => void
  onCopy: (cmd: string) => void
}) {
  // The API can send JSON null for empty lists (Go nil slices); coerce to arrays
  // so the `.length` reads below never crash the view.
  const provides = detail.provides ?? []
  const ports = detail.ports ?? []
  const dependencies = detail.dependencies ?? []
  const resources = detail.resources ?? []
  const installCommands = detail.installCommands ?? []
  const usedInScenarios = detail.usedInScenarios ?? []
  return (
    <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="modal-card" role="dialog" aria-modal="true" aria-label={`${detail.name} details`}>
        <button className="modal-close" aria-label="Close details" onClick={onClose}><Icon name="x" size={18} /></button>

        <div className="modal-header">
          <h2>{detail.name}</h2>
          <div className="modal-meta">
            <Badge variant="category">{detail.category}</Badge>
            <Badge variant={detail.installed ? 'running' : 'stopped'}>{detail.installed ? 'Installed' : 'Not Installed'}</Badge>
          </div>
        </div>

        {loading ? (
          <div className="loading" role="status">Loading…</div>
        ) : (
          <>
            {detail.description && (
              <div className="modal-section">
                <h3>What it is</h3>
                <p>{detail.description}</p>
                {detail.chart && <div className="hint-text">Helm chart: <code>{detail.chart}</code> · Namespace: <code>{detail.namespace}</code></div>}
              </div>
            )}

            {provides.length > 0 && (
              <div className="modal-section">
                <h3>What you can do with it</h3>
                <ul>{provides.map((p, i) => <li key={i}>{p}</li>)}</ul>
              </div>
            )}

            {(dependencies.length > 0 || ports.length > 0 || resources.length > 0) && (
              <div className="modal-section">
                <h3>Requires</h3>
                {dependencies.length > 0 && <div className="field-help">Depends on: {dependencies.join(', ')}</div>}
                {ports.length > 0 && <div className="field-help">Ports: {ports.join(', ')}</div>}
                {resources.length > 0 && <div className="field-help">Cluster resources: {resources.join(', ')}</div>}
              </div>
            )}

            {installCommands.length > 0 && (
              <div className="modal-section">
                <h3>How it's installed <span className="hint-text">(what the Install button runs)</span></h3>
                {installCommands.map((cmd, i) => (
                  <div key={i} className="cmd-block">
                    <code>{cmd}</code>
                    <button className="cmd-copy" onClick={() => onCopy(cmd)}>Copy</button>
                  </div>
                ))}
              </div>
            )}

            <div className="modal-section">
              <h3>Used in scenarios</h3>
              {usedInScenarios.length > 0 ? (
                <div className="prereq-chips">
                  {usedInScenarios.map(s => (
                    <Badge key={s.name} variant="category">{s.displayName || s.name}</Badge>
                  ))}
                </div>
              ) : (
                <div className="field-help">No scenario lists this as a prerequisite — it's available for ad-hoc use.</div>
              )}
            </div>
          </>
        )}

        <div className="card-footer">
          <button className="btn" onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  )
}
