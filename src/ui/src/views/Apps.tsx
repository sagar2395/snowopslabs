import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { AppInfo, AppDetail, AppFileRef, NotifyFn } from '../types'
import { Badge, DeployedBadge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import { useJobRunner } from '../hooks/useJobRunner'
import type { ConfirmRequest } from '../components/ConfirmDialog'

interface AppsProps {
  notify: NotifyFn
  requestConfirm: (req: ConfirmRequest) => void
}

/** Turns a raw HPA metric name into readable words, e.g.
 *  "go_api_requests_per_second" → "go api requests per second". */
function prettyMetricName(name?: string): string {
  return (name || 'metric').replace(/_/g, ' ')
}

/** Plain-English tooltip explaining the autoscaler's driving metric so a user
 *  doesn't have to decode "0.3 / 25" — what it is, and when it scales. */
function hpaMetricTitle(hpa: NonNullable<AppInfo['hpa']>): string {
  const metric = prettyMetricName(hpa.metricName)
  return `Current ${hpa.metricCurrent || '0'} vs target ${hpa.metricTarget} of "${metric}", averaged across replicas. ` +
    `The autoscaler adds replicas when the average exceeds the target and removes them when it stays below.`
}

export function Apps({ notify, requestConfirm }: AppsProps) {
  // /status carries apps + domainSuffix + platform in one call; the Dashboard
  // shares this same cache key, so both views fetch it once.
  const { data, loading, loaded, loadError, refreshing, reload: load } = useApiQuery(qk.status, api.getStatus)
  const apps = data?.apps ?? []
  const suffix = data?.domainSuffix ?? ''
  const loggingActive = Boolean(data?.platform?.logging?.active)
  const { busy, run } = useJobRunner(notify)
  const [detail, setDetail] = useState<AppDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  async function openDetail(a: AppInfo) {
    setDetailLoading(true)
    setDetail({ name: a.name, description: '', tech: [], buildStrategy: '', deployStrategy: '', namespace: a.namespace || a.name })
    try {
      setDetail(await api.getAppDetail(a.name))
    } catch (e) {
      notify('error', 'Failed to load app details', e instanceof Error ? e.message : String(e))
      setDetail(null)
    } finally {
      setDetailLoading(false)
    }
  }

  async function copyText(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      notify('success', 'Copied to clipboard', '')
    } catch {
      notify('error', 'Copy failed', 'Clipboard access denied — copy manually.')
    }
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
    // Grafana 11+ Explore uses ?panes=<json> keyed by a pane id and resolves the
    // datasource by UID (the old ?left=<name> opens an empty Explore). "loki" is
    // the stable UID the Grafana datasource is provisioned with.
    const panes = {
      exp: {
        datasource: 'loki',
        queries: [{ refId: 'A', datasource: { type: 'loki', uid: 'loki' }, expr: `{namespace="${ns}"}` }],
        // 6h window: the lab's apps are quiet (log mostly at startup), so a 1h
        // default often shows "No logs found" even though logs exist.
        range: { from: 'now-6h', to: 'now' },
      },
    }
    window.open(`http://grafana.${suffix}/explore?orgId=1&schemaVersion=1&panes=${encodeURIComponent(JSON.stringify(panes))}`, '_blank', 'noopener')
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
                  {a.hpa && (
                    <div className="hpa-line" title={`Autoscaler ${a.hpa.name}: scales between ${a.hpa.minReplicas} and ${a.hpa.maxReplicas} replicas on ${a.hpa.metricName || 'a metric'}`}>
                      <Icon name="trending-up" size={13} className="hpa-icon" />
                      <span className="hpa-label">autoscaling</span>
                      <span className="hpa-stat">{a.hpa.currentReplicas}/{a.hpa.maxReplicas} replicas</span>
                      {a.hpa.desiredReplicas !== a.hpa.currentReplicas && (
                        <span className="hpa-stat hpa-pending">→ desired {a.hpa.desiredReplicas}</span>
                      )}
                      {a.hpa.metricTarget && (
                        <span className="hpa-stat hpa-metric" title={hpaMetricTitle(a.hpa)}>
                          {prettyMetricName(a.hpa.metricName)}: {a.hpa.metricCurrent || '0'} / {a.hpa.metricTarget}
                        </span>
                      )}
                      <span className="hpa-stat hpa-bounds">min {a.hpa.minReplicas} · max {a.hpa.maxReplicas}</span>
                    </div>
                  )}
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
                  <button className="btn btn-sm" onClick={() => openDetail(a)}>Details</button>
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

      {detail && (
        <AppDetailModal
          detail={detail}
          loading={detailLoading}
          onClose={() => setDetail(null)}
          onCopy={copyText}
        />
      )}
    </>
  )
}

/** Per-app details: what it is, its stack, and the real Dockerfile + Helm chart
 *  (with repo paths) so a learner can find the files and rebuild/redeploy it. */
function FileBlock({ label, file, onCopy }: { label: string; file: AppFileRef; onCopy: (t: string) => void }) {
  return (
    <div className="modal-section">
      <h3>{label} <span className="hint-text">{file.path}</span></h3>
      <div className="cmd-block cmd-block-multiline">
        <button className="cmd-copy" onClick={() => onCopy(file.content)}>Copy</button>
        <pre className="snippet-code">{file.content}</pre>
      </div>
      {file.truncated && <div className="field-help">Showing the first part of the file — open <code>{file.path}</code> for the full contents.</div>}
    </div>
  )
}

function AppDetailModal({ detail, loading, onClose, onCopy }: {
  detail: AppDetail
  loading: boolean
  onClose: () => void
  onCopy: (text: string) => void
}) {
  const tech = detail.tech ?? []
  const templates = detail.templates ?? []
  return (
    <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="modal-card" role="dialog" aria-modal="true" aria-label={`${detail.name} details`}>
        <button className="modal-close" aria-label="Close details" onClick={onClose}><Icon name="x" size={18} /></button>

        <div className="modal-header">
          <h2>{detail.name}</h2>
          <div className="modal-meta">
            {tech.map(t => <Badge key={t} variant="category">{t}</Badge>)}
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
              </div>
            )}

            <div className="modal-section">
              <h3>Build &amp; deploy</h3>
              {detail.buildStrategy && <div className="field-help">Build strategy: <code>{detail.buildStrategy}</code></div>}
              {detail.deployStrategy && <div className="field-help">Deploy strategy: <code>{detail.deployStrategy}</code></div>}
              <div className="field-help">Namespace: <code>{detail.namespace}</code></div>
              {detail.helmChartPath && <div className="field-help">Helm chart: <code>{detail.helmChartPath}</code></div>}
            </div>

            {detail.dockerfile && <FileBlock label="Dockerfile" file={detail.dockerfile} onCopy={onCopy} />}
            {detail.valuesFile && <FileBlock label="Helm values (used by Deploy)" file={detail.valuesFile} onCopy={onCopy} />}
            {detail.chartYaml && <FileBlock label="Chart.yaml" file={detail.chartYaml} onCopy={onCopy} />}

            {templates.length > 0 && (
              <div className="modal-section">
                <h3>Helm templates</h3>
                <ul>{templates.map(t => <li key={t}><code>{t}</code></li>)}</ul>
              </div>
            )}
          </>
        )}

        <div className="card-footer">
          <button className="btn" onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  )
}
