import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { NotifyFn, ResultRecord } from '../types'
import { Badge } from '../components/Badge'
import { ErrorState } from '../components/ErrorState'
import { TrendChart, type TrendSeries } from '../components/TrendChart'

interface ResultsProps {
  notify: NotifyFn
}

/** detectSeconds (MTTD) is recorded in an incident result's meta (W4-T04). */
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

/** Reads the objectives + per-check breakdown a scenario verification records
 *  (W4-T02), for the expandable detail. */
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
  // newest-first, applied in the query cache's select so every consumer sees
  // the same order without re-sorting on each render.
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

  // Simple stats
  const challenges = records.filter(r => r.kind === 'challenge' && r.score >= 0)
  const avgScore = average(challenges.map(r => r.score))
  const incidents = records.filter(r => r.kind === 'incident')
  const modules = records.filter(r => r.kind === 'module')
  const scenarios = records.filter(r => r.kind === 'scenario')

  // MTTR = resolve time (elapsedSeconds); MTTD = detectSeconds from meta. Both
  // are per incident resolution (W4-T04) — the numbers a responder cares about.
  const avgMttr = average(incidents.map(r => r.elapsedSeconds))
  const mttdValues = incidents.map(r => incidentDetectSeconds(r.meta)).filter((d): d is number => d != null)
  const avgMttd = average(mttdValues)

  // Trends read oldest→newest (records arrive newest-first from the cache). One
  // point per run; the chart component handles the 0/1/many-point cases.
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

  return (
    <>
      {loadError && (
        <div className="banner banner-warn" role="alert">
          Refresh failed ({loadError}) — showing last known data.
          <button className="btn btn-sm" style={{ marginLeft: 10 }} onClick={load} disabled={refreshing}>Retry</button>
        </div>
      )}

      {/* Summary stats */}
      {records.length > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: 12, marginBottom: 16 }}>
          {[
            { label: 'Total runs',       value: records.length },
            { label: 'Scenarios',        value: scenarios.length },
            { label: 'Incidents',        value: incidents.length },
            { label: 'Challenges',       value: challenges.length },
            { label: 'Modules done',     value: modules.length },
            ...(avgScore != null ? [{ label: 'Avg challenge score', value: `${avgScore}` }] : []),
            ...(avgMttr != null ? [{ label: 'Avg MTTR', value: formatElapsed(avgMttr) }] : []),
            ...(avgMttd != null ? [{ label: 'Avg MTTD', value: formatElapsed(avgMttd) }] : []),
          ].map(s => (
            <div key={s.label} className="card" style={{ padding: '12px 16px', textAlign: 'center' }}>
              <div style={{ fontSize: 28, fontWeight: 700 }}>{s.value}</div>
              <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4 }}>{s.label}</div>
            </div>
          ))}
        </div>
      )}

      {/* Trends — MTTR/MTTD over incidents and score over challenges (W7-T04).
          Only shown once there's something to plot; the chart itself still
          degrades gracefully for a single point. */}
      {(hasIncidentTrend || hasScoreTrend) && (
        <div style={{ display: 'grid', gridTemplateColumns: hasIncidentTrend && hasScoreTrend ? 'repeat(auto-fit, minmax(320px, 1fr))' : '1fr', gap: 12, marginBottom: 16 }}>
          {hasIncidentTrend && (
            <div className="card" style={{ padding: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 4 }}>Incident response over time</div>
              <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 8 }}>
                Time to detect and resolve, per incident (lower is better).
              </div>
              <TrendChart
                series={mttdSeries.points.length > 0 ? [mttrSeries, mttdSeries] : [mttrSeries]}
                formatValue={formatSecsShort}
                ariaLabel="Incident MTTR and MTTD trend, in seconds, per incident run over time"
              />
            </div>
          )}
          {hasScoreTrend && (
            <div className="card" style={{ padding: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 4 }}>Challenge scores over time</div>
              <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 8 }}>
                Score per graded challenge run (higher is better).
              </div>
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
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <select
              className="runtime-select"
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
              {refreshing ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </div>

        {visible.length === 0 ? (
          <div className="empty-state">
            {records.length === 0
              ? <>No runs recorded yet. Verify a scenario, complete an incident, challenge, or learn module to see history.</>
              : <>No {KIND_LABELS[kindFilter] ?? kindFilter} runs recorded yet.</>
            }
          </div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table className="modal-table" style={{ width: '100%' }}>
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
                    <td>
                      <Badge variant="category">{KIND_LABELS[r.kind] ?? r.kind}</Badge>
                    </td>
                    <td style={{ maxWidth: 260 }}>
                      {r.kind === 'scenario'
                        ? <ScenarioResultName record={r} />
                        : r.kind === 'challenge'
                        ? <ChallengeResultName record={r} />
                        : r.name}
                    </td>
                    <td style={{ color: 'var(--muted)', fontSize: 12, whiteSpace: 'nowrap' }}>
                      {formatDate(r.startedAt)}
                    </td>
                    <td style={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>
                      {formatElapsed(r.elapsedSeconds)}
                    </td>
                    <td>
                      <Badge variant={scoreVariant(r.score)}>{scoreLabel(r.score)}</Badge>
                    </td>
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
                    {records.some(rec => rec.user) && (
                      <td style={{ color: 'var(--muted)', fontSize: 12 }}>{r.user ?? '—'}</td>
                    )}
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

/** ChallengeResultName shows a challenge run's name with its check score
 *  breakdown (checksPassed/checksTotal), the per-check summary challenges
 *  record (W7-T04). */
function ChallengeResultName({ record }: { record: ResultRecord }) {
  const checks = challengeChecks(record.meta)
  const hints = record.hintsUsed ?? 0
  if (!checks && hints === 0) return <>{record.name}</>
  return (
    <div>
      <div>{record.name}</div>
      <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>
        {checks && <span>{checks.passed}/{checks.total} checks</span>}
        {checks && hints > 0 && ' · '}
        {hints > 0 && <span>{hints} hint{hints !== 1 ? 's' : ''}</span>}
      </div>
    </div>
  )
}

/** ScenarioResultName renders a scenario verification's name with an expandable
 *  breakdown of its objectives and per-check pass/fail (W4-T02). */
function ScenarioResultName({ record }: { record: ResultRecord }) {
  const { objectives, checks } = scenarioMeta(record.meta)
  const passed = checks.filter(c => c.pass).length
  if (objectives.length === 0 && checks.length === 0) return <>{record.name}</>
  return (
    <details className="result-detail">
      <summary style={{ cursor: 'pointer' }}>
        {record.name}
        {checks.length > 0 && (
          <span style={{ color: 'var(--muted)', fontSize: 12, marginLeft: 6 }}>
            ({passed}/{checks.length} checks)
          </span>
        )}
      </summary>
      <div style={{ padding: '8px 0 4px', fontSize: 13 }}>
        {objectives.length > 0 && (
          <>
            <div style={{ color: 'var(--muted)', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Objectives</div>
            <ul style={{ margin: '2px 0 8px', paddingLeft: 18 }}>
              {objectives.map((o, i) => <li key={i}>{o}</li>)}
            </ul>
          </>
        )}
        {checks.length > 0 && (
          <>
            <div style={{ color: 'var(--muted)', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Checks</div>
            <ul style={{ margin: '2px 0 0', paddingLeft: 4, listStyle: 'none' }}>
              {checks.map((c, i) => (
                <li key={i} style={{ display: 'flex', gap: 6, alignItems: 'baseline' }}>
                  <span style={{ color: c.pass ? 'var(--green, #16a34a)' : 'var(--red, #dc2626)' }}>{c.pass ? '✓' : '✗'}</span>
                  <span>{c.name}</span>
                  {!c.pass && c.detail && <span style={{ color: 'var(--muted)', fontSize: 12 }}>— {c.detail}</span>}
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </details>
  )
}
