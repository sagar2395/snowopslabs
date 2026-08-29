import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { NotifyFn, ResultRecord } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'
import { TrendChart, type TrendSeries } from '../components/TrendChart'

interface ResultsProps {
  notify: NotifyFn
}

/** detectSeconds (MTTD) is recorded in an incident result's meta. */
function incidentDetectSeconds(meta?: Record<string, unknown>): number | null {
  const d = Number(meta?.detectSeconds)
  return Number.isFinite(d) && d >= 0 ? d : null
}

/** checksPassed/checksTotal are recorded in a challenge result's meta. */
function challengeChecks(meta?: Record<string, unknown>): { passed: number; total: number } | null {
  const total = Number(meta?.checksTotal)
  const passed = Number(meta?.checksPassed)
  if (!Number.isFinite(total) || total <= 0) return null
  return { passed: Number.isFinite(passed) ? passed : 0, total }
}

/** Compact seconds for chart axes: "45s" under 90s, else whole minutes. */
function formatSecsShort(s: number) {
  return s < 90 ? `${Math.round(s)}s` : `${Math.round(s / 60)}m`
}

function average(xs: number[]): number | null {
  return xs.length ? Math.round(xs.reduce((a, b) => a + b, 0) / xs.length) : null
}

const KIND_LABELS: Record<string, string> = {
  incident:  'Incident',
  challenge: 'Challenge',
  module:    'Learn Module',
  scenario:  'Scenario',
}

interface CheckOutcome { name: string; pass: boolean; detail?: string }

/** Reads the objectives + per-check breakdown a scenario verification records. */
function scenarioMeta(meta?: Record<string, unknown>): { objectives: string[]; checks: CheckOutcome[] } {
  const objectives = Array.isArray(meta?.objectives) ? (meta!.objectives as string[]) : []
  const checks = Array.isArray(meta?.checks) ? (meta!.checks as CheckOutcome[]) : []
  return { objectives, checks }
}

function formatDate(iso: string) {
  try { return new Date(iso).toLocaleString() } catch { return iso }
}

function formatElapsed(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return s > 0 ? `${m}m ${s}s` : `${m}m`
}

function scoreVariant(score: number) {
  if (score < 0) return 'category'    // unscored (-1)
  if (score >= 80) return 'running'
  if (score >= 50) return 'category'
  return 'stopped'
}

function scoreLabel(score: number) {
  return score < 0 ? '—' : String(score)
}

const KINDS = ['all', 'scenario', 'incident', 'challenge', 'module'] as const
type KindFilter = typeof KINDS[number]

export function Results(_props: ResultsProps) {
  const [kindFilter, setKindFilter] = useState<KindFilter>('all')
  const { data: records = [], loading, loaded, loadError, refreshing, reload: load } = useApiQuery(
    qk.results,
    api.getResults,
    { select: data => [...(data ?? [])].sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime()) },
  )

  if (loading) return <div className="loading" role="status">Loading results…</div>

  if (loadError && !loaded) {
    return (
      <ErrorState
        title="Failed to load results"
        message={loadError}
        onRetry={load}
        retrying={refreshing}
      />
    )
  }

  const visible = kindFilter === 'all' ? records : records.filter(r => r.kind === kindFilter)

  const challenges = records.filter(r => r.kind === 'challenge' && r.score >= 0)
  const avgScore = average(challenges.map(r => r.score))
  const incidents = records.filter(r => r.kind === 'incident')
  const modules = records.filter(r => r.kind === 'module')
  const scenarios = records.filter(r => r.kind === 'scenario')

  const avgMttr = average(incidents.map(r => r.elapsedSeconds))
  const mttdValues = incidents.map(r => incidentDetectSeconds(r.meta)).filter((d): d is number => d != null)
  const avgMttd = average(mttdValues)

  const chrono = [...records].reverse()
  const chronoIncidents = chrono.filter(r => r.kind === 'incident')
  const mttrSeries: TrendSeries = {
    name: 'Resolve (MTTR)',
    color: 'var(--accent)',
    points: chronoIncidents.map(r => ({ y: r.elapsedSeconds, label: formatDate(r.startedAt) })),
  }
  const mttdSeries: TrendSeries = {
    name: 'Detect (MTTD)',
    color: 'var(--warning)',
    points: chronoIncidents
      .map(r => ({ d: incidentDetectSeconds(r.meta), r }))
      .filter((x): x is { d: number; r: ResultRecord } => x.d != null)
      .map(x => ({ y: x.d, label: formatDate(x.r.startedAt) })),
  }
  const scoreSeries: TrendSeries = {
    name: 'Challenge score',
    color: 'var(--success)',
    points: chrono.filter(r => r.kind === 'challenge' && r.score >= 0).map(r => ({ y: r.score, label: formatDate(r.startedAt) })),
  }
  const hasIncidentTrend = mttrSeries.points.length > 0
  const hasScoreTrend = scoreSeries.points.length > 0

  const stats: { label: string; value: string | number }[] = [
    { label: 'Total runs', value: records.length },
    { label: 'Scenarios', value: scenarios.length },
    { label: 'Incidents', value: incidents.length },
    { label: 'Challenges', value: challenges.length },
    { label: 'Modules done', value: modules.length },
    ...(avgScore != null ? [{ label: 'Avg challenge score', value: `${avgScore}` }] : []),
    ...(avgMttr != null ? [{ label: 'Avg MTTR', value: formatElapsed(avgMttr) }] : []),
    ...(avgMttd != null ? [{ label: 'Avg MTTD', value: formatElapsed(avgMttd) }] : []),
  ]

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

      {/* Summary stats */}
      {records.length > 0 && (
        <div className="stat-grid mb-4">
          {stats.map(s => (
            <div key={s.label} className="stat-tile">
              <div className="stat-value">{s.value}</div>
              <div className="stat-label">{s.label}</div>
            </div>
          ))}
        </div>
      )}

      {/* Trends — MTTR/MTTD over incidents and score over challenges. */}
      {(hasIncidentTrend || hasScoreTrend) && (
        <div className={`trend-grid mb-4${hasIncidentTrend && hasScoreTrend ? '' : ' is-single'}`}>
          {hasIncidentTrend && (
            <div className="card">
              <div className="field-label">Incident response over time</div>
              <div className="field-help mb-2">Time to detect and resolve, per incident (lower is better).</div>
              <TrendChart
                series={mttdSeries.points.length > 0 ? [mttrSeries, mttdSeries] : [mttrSeries]}
                formatValue={formatSecsShort}
                ariaLabel="Incident MTTR and MTTD trend, in seconds, per incident run over time"
              />
            </div>
          )}
          {hasScoreTrend && (
            <div className="card">
              <div className="field-label">Challenge scores over time</div>
              <div className="field-help mb-2">Score per graded challenge run (higher is better).</div>
              <TrendChart
                series={[scoreSeries]}
                ariaLabel="Challenge score trend, 0 to 100, per challenge run over time"
              />
            </div>
          )}
        </div>
      )}

      <div className="card">
        <div className="card-header">
          <span className="card-title">
            Run History ({visible.length}{kindFilter !== 'all' ? ` of ${records.length}` : ''})
          </span>
          <div className="card-tools">
            <select
              className="select"
              value={kindFilter}
              aria-label="Filter by kind"
              onChange={e => setKindFilter(e.target.value as KindFilter)}
            >
              <option value="all">All types</option>
              {KINDS.filter(k => k !== 'all').map(k => (
                <option key={k} value={k}>{KIND_LABELS[k] ?? k}</option>
              ))}
            </select>
            <button className="btn btn-sm" onClick={load} disabled={refreshing}>
              <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </div>

        {visible.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="results" size={24} /></span>
            {records.length === 0
              ? <div>No runs recorded yet. Verify a scenario, complete an incident, challenge, or learn module to see history.</div>
              : <div>No {KIND_LABELS[kindFilter] ?? kindFilter} runs recorded yet.</div>
            }
          </div>
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Type</th>
                  <th>Name</th>
                  <th>Date</th>
                  <th>Duration</th>
                  <th>Score</th>
                  <th>Outcome</th>
                  {records.some(r => r.user) && <th>User</th>}
                </tr>
              </thead>
              <tbody>
                {visible.map((r, i) => (
                  <tr key={i}>
                    <td><Badge variant="category">{KIND_LABELS[r.kind] ?? r.kind}</Badge></td>
                    <td className="td-name">
                      {r.kind === 'scenario'
                        ? <ScenarioResultName record={r} />
                        : r.kind === 'challenge'
                        ? <ChallengeResultName record={r} />
                        : r.name}
                    </td>
                    <td className="td-muted td-nowrap">{formatDate(r.startedAt)}</td>
                    <td className="tnum td-nowrap">{formatElapsed(r.elapsedSeconds)}</td>
                    <td><Badge variant={scoreVariant(r.score)}>{scoreLabel(r.score)}</Badge></td>
                    <td>
                      <Badge variant={
                        r.outcome === 'passed' || r.outcome === 'resolved' || r.outcome === 'auto-resolved'
                          ? 'running'
                          : r.outcome === 'failed' || r.outcome === 'aborted'
                          ? 'stopped'
                          : 'category'
                      }>
                        {r.outcome}
                      </Badge>
                    </td>
                    {records.some(rec => rec.user) && <td className="td-muted">{r.user ?? '—'}</td>}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  )
}

function ChallengeResultName({ record }: { record: ResultRecord }) {
  const checks = challengeChecks(record.meta)
  const hints = record.hintsUsed ?? 0
  if (!checks && hints === 0) return <>{record.name}</>
  return (
    <div>
      <div>{record.name}</div>
      <div className="td-muted mt-1">
        {checks && <span>{checks.passed}/{checks.total} checks</span>}
        {checks && hints > 0 && ' · '}
        {hints > 0 && <span>{hints} hint{hints !== 1 ? 's' : ''}</span>}
      </div>
    </div>
  )
}

function ScenarioResultName({ record }: { record: ResultRecord }) {
  const { objectives, checks } = scenarioMeta(record.meta)
  const passed = checks.filter(c => c.pass).length
  if (objectives.length === 0 && checks.length === 0) return <>{record.name}</>
  return (
    <details className="result-detail">
      <summary>
        {record.name}
        {checks.length > 0 && <span className="td-muted"> ({passed}/{checks.length} checks)</span>}
      </summary>
      <div className="result-detail-body">
        {objectives.length > 0 && (
          <>
            <div className="meta-label">Objectives</div>
            <ul className="result-list">
              {objectives.map((o, i) => <li key={i}>{o}</li>)}
            </ul>
          </>
        )}
        {checks.length > 0 && (
          <>
            <div className="meta-label mt-2">Checks</div>
            <ul className="check-list">
              {checks.map((c, i) => (
                <li key={i}>
                  <span className={c.pass ? 'check-pass' : 'check-fail'}>
                    <Icon name={c.pass ? 'check' : 'x'} size={14} />
                  </span>
                  <span>{c.name}</span>
                  {!c.pass && c.detail && <span className="td-muted">— {c.detail}</span>}
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </details>
  )
}
