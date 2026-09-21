import { useState } from 'react'
import { api } from '../api/client'
import { qk } from '../lib/queryClient'
import { useApiQuery } from '../hooks/useApiQuery'
import type { Comparison, ComparisonMetric, NotifyFn } from '../types'
import { ErrorState } from '../components/ErrorState'
import { Icon } from '../components/Icon'

interface CompareProps {
  notify: NotifyFn
}

/** Raw Prometheus value → the number a reader sees, using the scale and
 *  precision the server sent with the metric. */
function formatValue(m: ComparisonMetric, raw: number) {
  const n = (raw * m.scale).toFixed(m.digits)
  return m.unit ? `${n} ${m.unit}` : n
}

function formatDuration(seconds: number) {
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m${seconds % 60}s`
}

function formatWhen(iso: string) {
  try { return new Date(iso).toLocaleString() } catch { return iso }
}

type Verdict = 'better' | 'worse' | 'same' | 'none'

/** How a value compares with the baseline.
 *
 *  The verdict is a word and an icon, never a colour on its own: colour alone
 *  fails anyone who cannot distinguish it, and "20% worse" on latency reads as
 *  an improvement if the only cue is a green tint. */
function compareToBaseline(m: ComparisonMetric, base: number | undefined, v: number): {
  verdict: Verdict; pct: number
} {
  if (base === undefined || base === 0) return { verdict: 'none', pct: 0 }
  const pct = ((v - base) / base) * 100
  if (m.neutral) return { verdict: 'none', pct }
  if (Math.abs(pct) < 0.05) return { verdict: 'same', pct }
  const improved = m.lowerBetter ? pct < 0 : pct > 0
  return { verdict: improved ? 'better' : 'worse', pct }
}

const VERDICT_ICON = { better: 'check-circle', worse: 'alert-triangle', same: 'dot', none: 'dot' } as const

export function Compare(_props: CompareProps) {
  const { data: comparisons = [], loading, loaded, loadError, refreshing, reload: load } =
    useApiQuery(qk.comparisons, api.getComparisons)
  const [selectedId, setSelectedId] = useState<string | null>(null)

  if (loading) return <div className="loading" role="status">Loading comparisons…</div>

  if (loadError && !loaded) {
    return <ErrorState title="Failed to load comparisons" message={loadError} onRetry={load} retrying={refreshing} />
  }

  const selected: Comparison | undefined =
    comparisons.find(c => c.id === selectedId) ?? comparisons[0]

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
          <span className="card-title">How a comparison works</span>
        </div>
        <p className="page-intro">
          A comparison runs one scenario against two or more applications under identical load and reports
          the same metrics for each, so a difference in the table is a difference between the apps rather
          than between two runs. Only one app is under load at a time, the warmup is excluded from the
          measured window, and the scenario is torn down between apps — a run where any app fails is not
          reported at all.
        </p>
        <p className="page-intro">
          Each run mutates the cluster for several minutes per app, so it is started from the terminal.
          Results appear here as soon as a run finishes.
        </p>
        <dl className="cli-recipe">
          <dt>Compare two apps under the autoscaling scenario</dt>
          <dd><code>labctl compare run autoscaling-under-load --apps go-api,java-api</code></dd>
          <dt>Longer window, higher rate (the window has a 2m floor — a rate needs two scrapes)</dt>
          <dd><code>labctl compare run autoscaling-under-load --apps go-api,java-api --rps 80 --window 5m</code></dd>
          <dt>List past runs, or print one as a table</dt>
          <dd><code>labctl compare list</code><br /><code>labctl compare show &lt;id&gt;</code></dd>
        </dl>
        <p className="field-help">
          The first app named is the baseline every other app is measured against.
          {' '}<code>--profile</code>, <code>--rps</code>, <code>--warmup</code> and <code>--window</code> apply
          identically to every app in the run.
        </p>
      </div>

      <div className="card">
        <div className="card-header">
          <span className="card-title">Comparisons ({comparisons.length})</span>
          <button className="btn btn-sm" onClick={load} disabled={refreshing}>
            <Icon name="refresh" size={14} />{refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {comparisons.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon"><Icon name="compare" size={24} /></span>
            <div>No comparisons recorded yet.</div>
            <p className="empty-hint">
              A comparison runs one scenario against two or more apps under identical
              load. It takes several minutes per app, so it runs from the terminal:
            </p>
            <code className="empty-code">labctl compare run autoscaling-under-load --apps go-api,java-api</code>
          </div>
        ) : (
          <ul className="run-picker" aria-label="Recorded comparisons">
            {comparisons.map(c => (
              <li key={c.id}>
                <button
                  className={`run-pick${c.id === selected?.id ? ' active' : ''}`}
                  onClick={() => setSelectedId(c.id)}
                  aria-current={c.id === selected?.id}
                >
                  <span className="run-pick-scenario">{c.scenario}</span>
                  <span className="run-pick-apps">{c.apps.join(' vs ')}</span>
                  <span className="run-pick-when">{formatWhen(c.startedAt)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {selected && <ComparisonTable c={selected} />}
    </>
  )
}

function ComparisonTable({ c }: { c: Comparison }) {
  const baseline = c.measurements[0]

  return (
    <div className="card">
      <div className="card-header">
        <span className="card-title">{c.scenario}</span>
      </div>

      {/* The conditions are part of the result. A measurement without them
          cannot be compared with anything, so they sit above the numbers
          rather than in a tooltip. */}
      <dl className="conditions" aria-label="Conditions every app was held to">
        <div><dt>Load</dt><dd>{c.profile} at {c.rps} req/s</dd></div>
        <div><dt>Warmup</dt><dd>{formatDuration(c.warmupSeconds)} <span className="muted">excluded</span></dd></div>
        <div><dt>Measured</dt><dd>{formatDuration(c.windowSeconds)}</dd></div>
        <div><dt>Order</dt><dd>one app at a time</dd></div>
      </dl>

      <div className="table-scroll">
        <table className="data-table compare-table">
          <caption className="sr-only">
            {c.scenario} measured across {c.apps.join(', ')}. {c.apps[0]} is the baseline;
            every other column shows its difference from it.
          </caption>
          <thead>
            <tr>
              <th scope="col">Metric</th>
              {c.measurements.map((m, i) => (
                <th scope="col" key={m.app}>
                  {m.app}
                  {i === 0 && <span className="col-note"> baseline</span>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {c.metrics.map(metric => {
              const present = c.measurements
                .map(m => m.values[metric.key])
                .filter((v): v is number => typeof v === 'number')
              if (present.length === 0) return null
              const max = Math.max(...present, 0)

              return (
                <tr key={metric.key}>
                  <th scope="row" className="metric-name">{metric.label}</th>
                  {c.measurements.map((m, i) => {
                    const v = m.values[metric.key]
                    if (typeof v !== 'number') {
                      return <td key={m.app} className="tnum">—<span className="sr-only"> not measurable</span></td>
                    }
                    const { verdict, pct } = i === 0
                      ? { verdict: 'none' as Verdict, pct: 0 }
                      : compareToBaseline(metric, baseline?.values[metric.key], v)

                    return (
                      <td key={m.app} className="tnum compare-cell">
                        {/* Magnitude within the row, so rows with different
                            units stay comparable across columns but never
                            against each other. */}
                        <span
                          className="compare-bar"
                          style={{ width: max > 0 ? `${Math.max((v / max) * 100, 2)}%` : '0%' }}
                          aria-hidden="true"
                        />
                        <span className="compare-figure">{formatValue(metric, v)}</span>
                        {verdict !== 'none' && (
                          <span className={`compare-delta delta-${verdict}`}>
                            <Icon name={VERDICT_ICON[verdict]} size={12} aria-hidden="true" />
                            {verdict === 'same' ? 'same' : `${Math.abs(pct).toFixed(0)}% ${verdict}`}
                          </span>
                        )}
                      </td>
                    )
                  })}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      <p className="card-footnote">
        These are measurements, not a grade — the scenario's own checks decide pass and fail.
        A dash means the cluster exported no series for that metric.
      </p>
    </div>
  )
}
