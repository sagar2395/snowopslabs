// SPDX-License-Identifier: Apache-2.0
//
// The files a scenario or fault shows the learner. Both views render snippets
// the same way, so what an exercise looks like is decided once, here.
import type { ContentSnippet, NotifyFn, WorkloadBinding } from '../types'
import { Badge } from './Badge'
import { Collapsible } from './Collapsible'

interface SnippetListProps {
  snippets: ContentSnippet[]
  notify: NotifyFn
  /** The app the bodies were rendered for. */
  workload?: WorkloadBinding
  /** Maps a snippet's path to the path shown beside its title. */
  sourcePath?: (path: string) => string
}

export function SnippetList({ snippets, notify, workload, sourcePath = p => p }: SnippetListProps) {
  const hasScripts = snippets.some(sn => sn.path?.endsWith('.sh'))

  async function copy(text: string, what: string) {
    try {
      await navigator.clipboard.writeText(text)
      notify('success', `Copied ${what}`, '')
    } catch {
      notify('error', 'Copy failed', 'Clipboard access denied — select the text and copy it manually.')
    }
  }

  return (
    <>
      {workload && (
        <p className="field-help snippet-binding">
          Shown for <code>{workload.app}</code> in namespace <code>{workload.namespace}</code>.
          {hasScripts && (
            <> Scripts take the app as <code>--app</code> and <code>--namespace</code>; the commands on the
              Explore tab fill both in.</>
          )}
        </p>
      )}
      <div className="collapse-group">
        {snippets.map((sn, i) => (
          <Collapsible
            key={`${sn.label}-${i}`}
            title={
              <>
                {sn.label}
                {sn.exercise && <> <Badge variant="pending">You apply this</Badge></>}
              </>
            }
            aside={sn.path ? sourcePath(sn.path) : undefined}
            defaultOpen={i === 0}
          >
            {sn.description && <div className="snippet-desc">{sn.description}</div>}
            {sn.exercise && (
              <div className="snippet-exercise">
                <span>Nothing installs this for you — applying it is the exercise, and verify grades the result.</span>
                {sn.applyCommand && (
                  <button className="btn btn-sm" onClick={() => copy(sn.applyCommand!, 'the apply command')}>
                    Copy apply command
                  </button>
                )}
              </div>
            )}
            {sn.yaml && (
              <>
                <div className="snippet-head">
                  <span className="hint-text">{sn.yaml.trimEnd().split('\n').length} lines</span>
                  <button className="cmd-copy" onClick={() => copy(sn.yaml!, 'to clipboard')}>Copy</button>
                </div>
                <pre className="snippet-code">{sn.yaml}</pre>
              </>
            )}
          </Collapsible>
        ))}
      </div>
    </>
  )
}
