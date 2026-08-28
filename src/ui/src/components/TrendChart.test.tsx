// SPDX-License-Identifier: Apache-2.0
//
// TrendChart has to survive the three scales the W7-T04 exit criteria name:
// empty (0), degenerate (1 point), and scale (1000). These assert it renders
// something sensible at each without throwing, and that the empty case is a
// designed state rather than a blank SVG.
import { render, screen } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { TrendChart, type TrendSeries } from './TrendChart'

function series(ys: number[], name = 'S', color = 'var(--accent)'): TrendSeries {
  return { name, color, points: ys.map(y => ({ y })) }
}

describe('TrendChart', () => {
  it('renders a designed empty state with zero points', () => {
    render(<TrendChart series={[series([])]} ariaLabel="empty chart" />)
    expect(screen.getByText(/not enough data/i)).toBeInTheDocument()
    // No plotted svg image when there's nothing to plot.
    expect(screen.queryByRole('img', { name: /empty chart$/ })).not.toBeInTheDocument()
  })

  it('renders a single marker (no line) for one point', () => {
    const { container } = render(<TrendChart series={[series([42])]} ariaLabel="one point" />)
    expect(screen.getByRole('img', { name: 'one point' })).toBeInTheDocument()
    // A single point cannot form a line: exactly one circle, no <path> line.
    expect(container.querySelectorAll('circle')).toHaveLength(1)
    expect(container.querySelector('path')).toBeNull()
  })

  it('scales to 1000 points without drawing 1000 markers', () => {
    const many = Array.from({ length: 1000 }, (_, i) => (i * 37) % 100)
    const { container } = render(<TrendChart series={[series(many)]} ariaLabel="thousand points" />)
    expect(screen.getByRole('img', { name: 'thousand points' })).toBeInTheDocument()
    // The line is drawn as a single path; markers are dropped past the limit.
    expect(container.querySelectorAll('path')).toHaveLength(1)
    expect(container.querySelectorAll('circle').length).toBeLessThan(1000)
  })

  it('draws a legend only when more than one series is present', () => {
    const { rerender } = render(<TrendChart series={[series([1, 2, 3])]} ariaLabel="one series" />)
    expect(screen.queryByText('S')).not.toBeInTheDocument()
    rerender(
      <TrendChart
        series={[series([1, 2, 3], 'A'), series([3, 2, 1], 'B', 'var(--warning)')]}
        ariaLabel="two series"
      />,
    )
    expect(screen.getByText('A')).toBeInTheDocument()
    expect(screen.getByText('B')).toBeInTheDocument()
  })

  it('applies the value formatter to the y-axis labels', () => {
    render(<TrendChart series={[series([60])]} formatValue={s => `${s}s`} ariaLabel="formatted" />)
    // A flat single value renders synthetic min/max around it; each axis label
    // carries the formatter's suffix.
    expect(screen.getAllByText(/\d+s$/).length).toBeGreaterThan(0)
  })
})
