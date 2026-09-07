// SPDX-License-Identifier: Apache-2.0
//
// Disclosure for the bulky parts of a detail modal — a manifest snippet, a
// Dockerfile, a Helm values file. Native <details>/<summary> so it works with
// find-in-page, keyboard and screen readers without any state of our own.
import { Icon } from './Icon'

interface CollapsibleProps {
  title: React.ReactNode
  /** Right-aligned secondary text in the summary, e.g. a source path. */
  aside?: React.ReactNode
  defaultOpen?: boolean
  children: React.ReactNode
}

export function Collapsible({ title, aside, defaultOpen = false, children }: CollapsibleProps) {
  return (
    <details className="collapse" open={defaultOpen}>
      <summary>
        <Icon name="chevron-right" size={15} className="collapse-chevron" />
        <span className="collapse-title">{title}</span>
        {aside && <span className="collapse-aside hint-text">{aside}</span>}
      </summary>
      <div className="collapse-body">{children}</div>
    </details>
  )
}
