// SPDX-License-Identifier: Apache-2.0
//
// TrendChart — a small, dependency-free SVG line chart for the Results view
// (W7-T04). It has to read correctly at three very different scales, which the
// exit criteria call out explicitly:
//
//   - 0 points  → the caller renders an empty state; a series with no points is
//                 simply skipped, and an all-empty chart shows a muted note.
//   - 1 point   → a single value can't draw a line, so we render a lone marker
//                 on the mid-line rather than a degenerate zero-length path.
//   - 1000 pts  → the polyline scales to the plot width and markers are dropped
//                 past a threshold so the line stays legible instead of a blob.
//
// Points are ordered oldest→newest by the caller. The y-axis is auto-scaled to
// the combined range of every series with a little headroom; the x-axis is
// positional (evenly spaced by index), which is what a "trend over runs" reads
// as — we are not claiming a true time axis.

export interface TrendPoint {
  /** Value plotted on the y-axis. */
  y: number
  /** Optional human label for the point (e.g. a date), used in the title. */
  label?: string
}

export interface TrendSeries {
  name: string
  color: string
  points: TrendPoint[]
}

interface TrendChartProps {
  series: TrendSeries[]
  /** Formats a y value for the axis labels and point titles (e.g. "45s"). */
  formatValue?: (y: number) => string
  height?: number
  /** Accessible description of what the chart shows. */
  ariaLabel: string
}

const W = 600
const PAD = { top: 12, right: 12, bottom: 12, left: 48 }
// Above this many points per series, individual markers become noise.
const MARKER_LIMIT = 60

export function TrendChart({ series, formatValue = String, height = 160, ariaLabel }: TrendChartProps) {
  const H = height
  const plotW = W - PAD.left - PAD.right
  const plotH = H - PAD.top - PAD.bottom

  const nonEmpty = series.filter(s => s.points.length > 0)
  const maxLen = nonEmpty.reduce((m, s) => Math.max(m, s.points.length), 0)

  if (nonEmpty.length === 0 || maxLen === 0) {
    return (
      <div className="empty-state" style={{ padding: '24px 0' }} role="img" aria-label={`${ariaLabel} — no data yet`}>
        Not enough data to chart yet.
      </div>
    )
  }

  // Combined y-range across all series, with a touch of headroom so the peak
  // isn't glued to the top edge. A flat series (all equal) gets a synthetic
  // span so it renders on the mid-line instead of collapsing.
  let yMin = Infinity
  let yMax = -Infinity
  for (const s of nonEmpty) {
    for (const p of s.points) {
      if (p.y < yMin) yMin = p.y
      if (p.y > yMax) yMax = p.y
    }
  }
  if (yMin === yMax) { yMin -= 1; yMax += 1 }
  const yPad = (yMax - yMin) * 0.1
  yMin = Math.max(0, yMin - yPad)
  yMax = yMax + yPad

  // x is positional by index; a single point sits in the middle so it doesn't
  // pin to the left edge.
  const xAt = (i: number, len: number) =>
    PAD.left + (len <= 1 ? plotW / 2 : (i / (len - 1)) * plotW)
  const yAt = (v: number) =>
    PAD.top + plotH - ((v - yMin) / (yMax - yMin)) * plotH

  const showMarkers = maxLen <= MARKER_LIMIT

  return (
    <div style={{ width: '100%' }}>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        width="100%"
        height={H}
        preserveAspectRatio="none"
        role="img"
        aria-label={ariaLabel}
        style={{ display: 'block', overflow: 'visible' }}
      >
        {/* y-axis gridlines + labels at min / mid / max */}
        {[yMax, (yMin + yMax) / 2, yMin].map((v, i) => {
          const y = yAt(v)
          return (
            <g key={i}>
              <line x1={PAD.left} y1={y} x2={W - PAD.right} y2={y} stroke="var(--border)" strokeWidth={1} strokeDasharray={i === 2 ? undefined : '3 3'} />
              <text x={PAD.left - 8} y={y + 4} textAnchor="end" fontSize={11} fill="var(--muted)" fontVariant="tabular-nums">
                {formatValue(Math.round(v))}
              </text>
            </g>
          )
        })}

        {nonEmpty.map(s => {
          const len = s.points.length
          const coords = s.points.map((p, i) => ({ x: xAt(i, len), y: yAt(p.y), p }))
          const path = coords.map((c, i) => `${i === 0 ? 'M' : 'L'}${c.x.toFixed(1)},${c.y.toFixed(1)}`).join(' ')
          return (
            <g key={s.name}>
              {len > 1 && <path d={path} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />}
              {(showMarkers || len === 1) && coords.map((c, i) => (
                <circle key={i} cx={c.x} cy={c.y} r={len === 1 ? 4 : 3} fill={s.color}>
                  <title>{`${s.name}: ${formatValue(c.p.y)}${c.p.label ? ` (${c.p.label})` : ''}`}</title>
                </circle>
              ))}
            </g>
          )
        })}
      </svg>

      {/* Legend — only when more than one series shares the plot. */}
      {nonEmpty.length > 1 && (
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', marginTop: 6, fontSize: 12, color: 'var(--muted)' }}>
          {nonEmpty.map(s => (
            <span key={s.name} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <span aria-hidden style={{ width: 10, height: 3, borderRadius: 2, background: s.color, display: 'inline-block' }} />
              {s.name}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
