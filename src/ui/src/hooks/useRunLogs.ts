// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { errMessage } from '../lib/errors'
import type { RunLogLine } from '../types'

/** useRunLogs tails a run's transcript by polling the cursor-forward logs
 *  endpoint and accumulating lines, exactly as `labctl runs logs --follow`
 *  does. Reading from a cursor (not a live socket) is what makes it correct:
 *  a reconnect or a slow render can never skip or duplicate a line (ADR-0006).
 *  Polling stops as soon as the run is done. */
export function useRunLogs(id: string | null) {
  const [lines, setLines] = useState<RunLogLine[]>([])
  const [done, setDone] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setLines([])
    setDone(false)
    setError(null)
    if (!id) return

    let cancelled = false
    let after = 0
    let timer: ReturnType<typeof setTimeout>

    const tick = async () => {
      try {
        const res = await api.getRunLogs(id, after)
        if (cancelled) return
        if (res.lines.length > 0) {
          after = res.nextAfter
          setLines(prev => [...prev, ...res.lines])
        }
        if (res.done) {
          setDone(true)
          return
        }
      } catch (e) {
        if (cancelled) return
        setError(errMessage(e))
        return
      }
      timer = setTimeout(tick, 1000)
    }
    void tick()

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [id])

  return { lines, done, error }
}
