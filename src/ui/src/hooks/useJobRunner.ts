import { useState, useRef, useEffect, useCallback } from 'react'
import { trackJob } from '../lib/jobs'
import type { ActionAccepted, NotifyFn } from '../types'

/** Runs async API actions with job-aware busy state:
 *  - a key stays busy from the POST until the job's action_end event arrives
 *    (not a blind setTimeout), so buttons can't be double-clicked mid-run;
 *  - a success/failure toast fires when the job actually completes;
 *  - completion survives tab switches (callbacks guard against unmount). */
export function useJobRunner(notify: NotifyFn) {
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const busyRef = useRef<Set<string>>(new Set())
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  const setKey = useCallback((key: string, v: boolean) => {
    if (v) busyRef.current.add(key)
    else busyRef.current.delete(key)
    if (mountedRef.current) setBusy(prev => ({ ...prev, [key]: v }))
  }, [])

  const run = useCallback(async (
    key: string,
    label: string,
    fn: () => Promise<ActionAccepted>,
    onDone?: (ok: boolean) => void,
  ) => {
    if (busyRef.current.has(key)) return // double-submit guard
    setKey(key, true)
    try {
      const res = await fn()
      notify('info', `${label} started`, 'Progress is streaming in the output panel below.')
      trackJob(res.jobId, (ok, error) => {
        setKey(key, false)
        if (ok) notify('success', `${label} completed`)
        else notify('error', `${label} failed`, error || 'See the command output panel for details.')
        onDone?.(ok)
      })
    } catch (e) {
      setKey(key, false)
      notify('error', `${label} failed`, e instanceof Error ? e.message : String(e))
    }
  }, [notify, setKey])

  return { busy, run }
}
