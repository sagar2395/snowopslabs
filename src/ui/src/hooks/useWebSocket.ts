import { useEffect, useRef, useCallback } from 'react'
import type { WSMessage, ActionEvent, ClusterInfo } from '../types'

export type WSStatus = 'connecting' | 'connected' | 'disconnected'

interface UseWebSocketOptions {
  onActionEvent: (event: ActionEvent) => void
  onStatusChange: (status: WSStatus) => void
  /** Live cluster status frames pushed by the server every 5 s
   *  (and immediately on connect). `null` when the cluster is unreachable. */
  onClusterStatus?: (info: ClusterInfo | null) => void
}

const MAX_BACKOFF_MS = 15_000

/** Opens and manages a persistent WebSocket connection to /api/ws.
 *  Reconnects automatically with exponential backoff + jitter. */
export function useWebSocket({ onActionEvent, onStatusChange, onClusterStatus }: UseWebSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null)
  const retryRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const attemptRef = useRef(0)
  const mountedRef = useRef(true)

  const connect = useCallback(() => {
    if (!mountedRef.current) return
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    let ws: WebSocket
    try {
      ws = new WebSocket(`${proto}//${location.host}/api/ws`)
    } catch {
      scheduleRetry()
      return
    }
    wsRef.current = ws

    function scheduleRetry() {
      if (!mountedRef.current) return
      onStatusChange('disconnected')
      const base = Math.min(MAX_BACKOFF_MS, 1000 * 2 ** attemptRef.current)
      const delay = base + Math.floor(Math.random() * 400) // jitter avoids thundering herd
      attemptRef.current += 1
      retryRef.current = setTimeout(connect, delay)
    }

    ws.onopen = () => {
      if (!mountedRef.current) return
      attemptRef.current = 0 // reset backoff on a good connection
      onStatusChange('connected')
    }

    ws.onmessage = (e: MessageEvent<string>) => {
      try {
        const msg = JSON.parse(e.data) as WSMessage
        if (msg.type === 'action' && msg.data) {
          onActionEvent(msg.data as ActionEvent)
        } else if (msg.type === 'status') {
          onClusterStatus?.(msg.data as ClusterInfo | null)
        }
      } catch { /* ignore malformed frames */ }
    }

    ws.onclose = () => {
      if (wsRef.current === ws) wsRef.current = null
      scheduleRetry()
    }

    ws.onerror = () => ws.close()
  }, [onActionEvent, onStatusChange, onClusterStatus])

  useEffect(() => {
    mountedRef.current = true
    onStatusChange('connecting')
    connect()
    return () => {
      mountedRef.current = false
      if (retryRef.current) clearTimeout(retryRef.current)
      wsRef.current?.close()
    }
  }, [connect, onStatusChange])
}
