// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Badge, DeployedBadge, StatusBadge } from './Badge'

describe('Badge', () => {
  const variants = ['running', 'stopped', 'pending', 'category', 'runtime'] as const

  variants.forEach((variant) => {
    it(`renders the ${variant} variant class`, () => {
      render(<Badge variant={variant}>label</Badge>)
      expect(screen.getByText('label')).toHaveClass('badge', `badge-${variant}`)
    })
  })

  it('renders arbitrary children, not just strings', () => {
    render(
      <Badge variant="category">
        <span data-testid="child">nested</span>
      </Badge>,
    )
    expect(screen.getByTestId('child')).toBeInTheDocument()
  })
})

describe('StatusBadge', () => {
  it.each([
    [true, 'Active', 'badge-running'],
    [false, 'Inactive', 'badge-stopped'],
  ])('active=%s renders %s', (active, label, cls) => {
    render(<StatusBadge active={active as boolean} />)
    expect(screen.getByText(label as string)).toHaveClass(cls as string)
  })
})

describe('DeployedBadge', () => {
  it.each([
    [true, 'Deployed', 'badge-running'],
    [false, 'Not Deployed', 'badge-stopped'],
  ])('deployed=%s renders %s', (deployed, label, cls) => {
    render(<DeployedBadge deployed={deployed as boolean} />)
    expect(screen.getByText(label as string)).toHaveClass(cls as string)
  })
})
