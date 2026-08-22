import type { JobInfo } from '../types'

/** Callback fired exactly once when a tracked job finishes. */
export type JobEndCallback = (ok: boolean, error?: string) => void

/** Failsafe: if the action_end event is lost (e.g. server restarted) the
 *  callback still fires so the UI never shows a stuck busy state forever. */
const JOB_TIMEOUT_MS = 15 * 60_000

const tracked = new Map<string, { cb: JobEndCallback; timer: number }>()

/** Register interest in a job's completion. The callback fires when the
 *  matching action_end WebSocket event arrives, when reconcileJobs() finds
 *  the job already finished, or after a 15-minute failsafe timeout. */
export function trackJob(jobId: string, cb: JobEndCallback): void {
  if (!jobId) return
  const timer = window.setTimeout(() => {
    if (tracked.delete(jobId)) {
      cb(false, 'No completion event received — check the command output panel or refresh')
    }
  }, JOB_TIMEOUT_MS)
  tracked.set(jobId, { cb, timer })
}

/** Resolve a tracked job (idempotent — later calls for the same id no-op). */
export function completeJob(jobId: string | undefined, ok: boolean, error?: string): void {
  if (!jobId) return
  const t = tracked.get(jobId)
  if (!t) return
  tracked.delete(jobId)
  clearTimeout(t.timer)
  t.cb(ok, error)
}

/** After a WebSocket reconnect, settle any jobs that finished while events
 *  were not being received. Jobs still running stay tracked. */
export function reconcileJobs(jobs: JobInfo[]): void {
  for (const j of jobs) {
    if (j.status === 'running') continue
    completeJob(j.id, j.status === 'succeeded', j.error)
  }
}

/** Whether any jobs are still awaiting completion. */
export function hasTrackedJobs(): boolean {
  return tracked.size > 0
}
