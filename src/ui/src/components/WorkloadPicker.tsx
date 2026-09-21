// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { AppInfo } from '../types'

interface WorkloadPickerProps {
  /** The app selected when the dialog opens — the lab's current binding. */
  value: string
  onChange: (app: string) => void
  /** Capabilities the content needs the app to declare, from its prerequisites.
   *  Preflight refuses to activate against an app that is missing one, so these
   *  are blocking, not advisory. */
  requiredCapabilities?: string[]
  /** Apps the content names literally. When non-empty the content is pinned and
   *  the picker is read-only: choosing another app would break it. */
  pinnedApps?: string[]
  /** What the choice governs, e.g. "run against" or "break". */
  verb?: string
}

/** Chooses which application a scenario or fault acts on.
 *
 *  Content names its workload through {{.WorkloadName}} rather than an app
 *  (ADR-0014), so every scenario and fault runs against any conforming app. That
 *  was only reachable from the CLI's --app flag; here it is the first thing the
 *  activation dialog asks.
 *
 *  An app missing a capability the content requires is shown with the gap named
 *  and cannot be chosen: preflight refuses that activation outright, so offering
 *  it would trade a clear "this app cannot run this" for a failed run. */
export function WorkloadPicker({ value, onChange, requiredCapabilities = [], pinnedApps = [], verb = 'run against' }: WorkloadPickerProps) {
  const [apps, setApps] = useState<AppInfo[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    api.listApps()
      .then(a => { if (live) setApps(a) })
      .catch(e => { if (live) setError(e instanceof Error ? e.message : String(e)) })
    return () => { live = false }
  }, [])

  if (pinnedApps.length > 0) {
    return (
      <div className="field">
        <span className="field-label">Runs against</span>
        <div><code>{pinnedApps.join(', ')}</code></div>
        <span className="field-help">This content names its application directly, so it cannot be pointed at another one.</span>
      </div>
    )
  }

  const missing = (a: AppInfo) => requiredCapabilities.filter(c => !(a.capabilities ?? []).includes(c))
  const selected = apps?.find(a => a.name === value)
  const gaps = selected ? missing(selected) : []
  const eligible = (apps ?? []).filter(a => missing(a).length === 0).length

  return (
    <div className="field">
      <label className="field-label" htmlFor="workload-picker">Application to {verb}</label>
      {error && <span className="field-help field-error">Could not load the app list ({error}) — continuing with {value}.</span>}
      <select
        id="workload-picker"
        className="select"
        value={value}
        disabled={!apps}
        onChange={e => onChange(e.target.value)}
      >
        {!apps && <option value={value}>{value}</option>}
        {(apps ?? []).map(a => (
          <option key={a.name} value={a.name} disabled={missing(a).length > 0}>
            {a.name}
            {a.deployed ? '' : ' — not deployed'}
            {missing(a).length > 0 ? ` — cannot run this: no ${missing(a).join(', ')}` : ''}
          </option>
        ))}
      </select>
      {gaps.length > 0 ? (
        <span className="field-help field-error" role="alert">
          {value} does not declare {gaps.join(', ')}, so this is refused before anything installs.
          Pick an app that does, or add the capability to <code>apps/{value}/app.env</code> once {value} truly provides it.
        </span>
      ) : selected && !selected.deployed ? (
        <span className="field-help field-warn">{value} is not deployed yet — deploy it from the Apps tab first.</span>
      ) : (
        <span className="field-help">
          {requiredCapabilities.length > 0
            ? <>Needs {requiredCapabilities.join(', ')} — {eligible} of {apps?.length ?? 0} apps qualify. Namespace, port and metric come from the app itself.</>
            : <>The same content runs against any app that meets the contract. Namespace, port and metric come from the app itself.</>}
        </span>
      )}
    </div>
  )
}
