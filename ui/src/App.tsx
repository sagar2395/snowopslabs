import { useState, useCallback, useRef, useEffect, Component, type ReactNode } from 'react'
import { useWebSocket, type WSStatus } from './hooks/useWebSocket'
import type { ActionEvent, AuthStatus, ClusterInfo, LogEntry, Notification, NotifLevel } from './types'
import { NotificationList } from './components/Notification'
import { Login } from './components/Login'
import { LogPanel } from './components/LogPanel'
import { ConfirmDialog, type ConfirmRequest } from './components/ConfirmDialog'
import { Dashboard } from './views/Dashboard'
import { Scenarios } from './views/Scenarios'
import { Platform } from './views/Platform'
import { Apps } from './views/Apps'
import { Learn } from './views/Learn'
import { Challenges } from './views/Challenges'
import { Results } from './views/Results'
import { Leaderboard } from './views/Leaderboard'
import { api } from './api/client'
import { completeJob, reconcileJobs, hasTrackedJobs, trackJob } from './lib/jobs'

type Tab = 'dashboard' | 'scenarios' | 'platform' | 'apps' | 'learn' | 'challenges' | 'results' | 'leaderboard'

const TABS: { id: Tab; label: string }[] = [
  { id: 'dashboard',   label: 'Dashboard'   },
  { id: 'scenarios',   label: 'Scenarios'   },
  { id: 'platform',    label: 'Platform'    },
  { id: 'apps',        label: 'Apps'        },
  { id: 'learn',       label: 'Learn'       },
  { id: 'challenges',  label: 'Challenges'  },
  { id: 'results',     label: 'Results'     },
  { id: 'leaderboard', label: 'Leaderboard' },
]

let notifSeq = 0
let logSeq = 0

function nowHMS() {
  return new Date().toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

/** Catches render-time crashes in a view so one bad payload can't blank the
 *  whole UI — shows a reset affordance instead. */
class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) { return { error } }
  render() {
    if (this.state.error) {
      return (
        <div className="error-state" role="alert">
          <div className="error-state-title">⚠ This view crashed</div>
          <div className="error-state-message">{this.state.error.message}</div>
          <button className="btn btn-sm" onClick={() => this.setState({ error: null })}>Reload view</button>
        </div>
      )
    }
    return this.props.children
  }
}

function MainApp({ auth, onLogout }: { auth: AuthStatus; onLogout: () => void }) {
  const [tab, setTab] = useState<Tab>('dashboard')
  const [wsStatus, setWsStatus] = useState<WSStatus>('connecting')
  const [notifications, setNotifications] = useState<Notification[]>([])
  const [logEntries, setLogEntries] = useState<LogEntry[]>([])
  const [runtimes, setRuntimes] = useState<string[]>([])
  const [activeRuntime, setActiveRuntime] = useState('')
  const [runtimeBusy, setRuntimeBusy] = useState(false)
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)
  const [liveCluster, setLiveCluster] = useState<ClusterInfo | null>(null)
  const [lastStatusAt, setLastStatusAt] = useState<number | null>(null)
  // Bumped (debounced) whenever an action completes or the WS reconnects, so
  // the active view refreshes its data exactly when state may have changed.
  const [refreshTick, setRefreshTick] = useState(0)

  const notify = useCallback((level: NotifLevel, title: string, detail?: string) => {
    const id = ++notifSeq
    setNotifications(prev => [...prev, { id, level, title, detail }])
    if (level !== 'error') setTimeout(() => dismiss(id), 8000)
  }, [])

  function dismiss(id: number) {
    setNotifications(prev => prev.filter(n => n.id !== id))
  }

  const appendLog = useCallback((level: LogEntry['level'], text: string) => {
    setLogEntries(prev => {
      const next = [...prev, { id: ++logSeq, ts: nowHMS(), level, text }]
      return next.length > 500 ? next.slice(-500) : next
    })
  }, [])

  // Debounced refresh bump — coalesces bursts of action_end events
  // (e.g. platform-up's per-component sub-actions) into one data reload.
  const bumpTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const bumpRefresh = useCallback(() => {
    if (bumpTimer.current) return
    bumpTimer.current = setTimeout(() => {
      bumpTimer.current = null
      setRefreshTick(t => t + 1)
    }, 1200)
  }, [])
  useEffect(() => () => { if (bumpTimer.current) clearTimeout(bumpTimer.current) }, [])

  const onActionEvent = useCallback((ev: ActionEvent) => {
    switch (ev.type) {
      case 'action_start':
        appendLog('cmd', `▸ ${ev.action ?? ''}${ev.command ? `  (${ev.command})` : ''}`)
        break
      case 'action_output':
        appendLog(ev.stream === 'stderr' ? 'stderr' : 'output', ev.output ?? '')
        break
      case 'action_end': {
        const ok = (ev.exitCode ?? -1) === 0 && !ev.error
        appendLog(ok ? 'success' : 'error', ok
          ? `✓ ${ev.action ?? 'Command'} completed`
          : `✗ ${ev.action ?? 'Command'} failed${ev.error ? ': ' + ev.error : ''}`)
        completeJob(ev.id, ok, ev.error)
        bumpRefresh()
        break
      }
      case 'action_error':
        appendLog('error', ev.error ?? 'Unknown error')
        completeJob(ev.id, false, ev.error)
        break
    }
  }, [appendLog, bumpRefresh])

  const onClusterStatus = useCallback((info: ClusterInfo | null) => {
    setLiveCluster(info)
    setLastStatusAt(Date.now())
  }, [])

  // Detect disconnected → connected transitions to re-sync missed events.
  const prevWsStatus = useRef<WSStatus>('connecting')
  const onStatusChange = useCallback((s: WSStatus) => {
    const prev = prevWsStatus.current
    prevWsStatus.current = s
    setWsStatus(s)
    if (s === 'connected' && prev === 'disconnected') {
      notify('info', 'Reconnected', 'Live updates restored — refreshing data.')
      // Settle any jobs that finished while we were offline.
      if (hasTrackedJobs()) {
        api.getJobs().then(reconcileJobs).catch(() => { /* next event will settle them */ })
      }
      setRefreshTick(t => t + 1)
    }
  }, [notify])

  useWebSocket({ onActionEvent, onStatusChange, onClusterStatus })

  // Load runtimes once
  const runtimeLoaded = useRef(false)
  useEffect(() => {
    if (runtimeLoaded.current) return
    runtimeLoaded.current = true
    api.listRuntimes().then(list => {
      const names = list.map(r => r.name ?? r.profile ?? '').filter(Boolean)
      setRuntimes(names)
      const active = list.find(r => r.active)
      if (active) setActiveRuntime(active.name ?? active.profile ?? '')
    }).catch(e => {
      notify('error', 'Failed to load runtimes', e instanceof Error ? e.message : String(e))
    })
  }, [notify])

  function handleRuntimeChange(value: string) {
    if (!value || value === activeRuntime || runtimeBusy) return
    setConfirm({
      title: 'Switch runtime?',
      message: `Activating "${value}" may create or start a cluster, and the current runtime${activeRuntime ? ` ("${activeRuntime}")` : ''} stays as-is. This can take several minutes.`,
      confirmLabel: 'Switch runtime',
      danger: false,
      onConfirm: async () => {
        setRuntimeBusy(true)
        try {
          const res = await api.activateRuntime(value)
          notify('info', 'Runtime switching', `Activating ${value}…`)
          trackJob(res.jobId, (ok, error) => {
            setRuntimeBusy(false)
            if (ok) {
              setActiveRuntime(value)
              notify('success', 'Runtime switched', value)
            } else {
              notify('error', `Switching to ${value} failed`, error)
            }
          })
        } catch (e) {
          setRuntimeBusy(false)
          notify('error', `Switching to ${value} failed`, e instanceof Error ? e.message : String(e))
        }
      },
    })
  }

  // Keyboard navigation for tabs (Left/Right arrows)
  function onTabKeyDown(e: React.KeyboardEvent) {
    const idx = TABS.findIndex(t => t.id === tab)
    if (e.key === 'ArrowRight') {
      const next = TABS[(idx + 1) % TABS.length]
      setTab(next.id)
      document.getElementById(`tab-${next.id}`)?.focus()
    } else if (e.key === 'ArrowLeft') {
      const prev = TABS[(idx - 1 + TABS.length) % TABS.length]
      setTab(prev.id)
      document.getElementById(`tab-${prev.id}`)?.focus()
    }
  }

  const connDot = wsStatus === 'connected' ? 'green' : wsStatus === 'connecting' ? 'yellow' : 'red'
  const connLabel = wsStatus === 'connected' ? 'Connected' : wsStatus === 'connecting' ? 'Connecting…' : 'Disconnected'

  return (
    <div className="app-shell">
      <NotificationList items={notifications} onDismiss={dismiss} />
      <ConfirmDialog request={confirm} onClose={() => setConfirm(null)} />

      {/* Header */}
      <header className="app-header">
        <h1><span className="accent">labctl</span> Dashboard</h1>
        <div className="header-right">
          {runtimes.length > 0 && (
            <select
              className="runtime-select"
              value={activeRuntime}
              disabled={runtimeBusy}
              aria-label="Active runtime"
              onChange={e => handleRuntimeChange(e.target.value)}
            >
              {!activeRuntime && <option value="">select runtime…</option>}
              {runtimes.map(r => (
                <option key={r} value={r}>{r}{r === activeRuntime ? ' (active)' : ''}</option>
              ))}
            </select>
          )}
          <div className="conn-indicator" role="status" aria-live="polite">
            <span className={`dot dot-${connDot}`} aria-hidden="true" />
            <span>{connLabel}</span>
          </div>
          {auth.authEnabled && (
            <div className="auth-indicator">
              {auth.user && (
                <span className="auth-user">
                  {auth.user}{auth.role ? ` (${auth.role})` : ''}
                </span>
              )}
              <button className="btn btn-sm" onClick={onLogout}>Log out</button>
            </div>
          )}
        </div>
      </header>

      {/* Live-updates banner when the WebSocket is down */}
      {wsStatus === 'disconnected' && (
        <div className="banner banner-warn" role="alert">
          Live updates unavailable — reconnecting to the labctl server…
        </div>
      )}

      {/* Nav */}
      <nav className="nav-tabs" role="tablist" aria-label="Sections" onKeyDown={onTabKeyDown}>
        {TABS.map(t => (
          <button
            key={t.id}
            id={`tab-${t.id}`}
            role="tab"
            aria-selected={tab === t.id}
            tabIndex={tab === t.id ? 0 : -1}
            className={`nav-tab${tab === t.id ? ' active' : ''}`}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {/* Content */}
      <main className="main-content" role="tabpanel" aria-labelledby={`tab-${tab}`}>
        <ErrorBoundary>
          {tab === 'dashboard'  && (
            <Dashboard
              notify={notify}
              refreshTick={refreshTick}
              liveCluster={liveCluster}
              lastStatusAt={lastStatusAt}
              requestConfirm={setConfirm}
            />
          )}
          {tab === 'scenarios'  && <Scenarios   notify={notify} refreshTick={refreshTick} requestConfirm={setConfirm} />}
          {tab === 'platform'   && <Platform    notify={notify} refreshTick={refreshTick} requestConfirm={setConfirm} />}
          {tab === 'apps'       && <Apps        notify={notify} refreshTick={refreshTick} requestConfirm={setConfirm} />}
          {tab === 'learn'      && <Learn       notify={notify} refreshTick={refreshTick} />}
          {tab === 'challenges' && <Challenges  notify={notify} refreshTick={refreshTick} requestConfirm={setConfirm} />}
          {tab === 'results'    && <Results     notify={notify} refreshTick={refreshTick} />}
          {tab === 'leaderboard' && <Leaderboard notify={notify} refreshTick={refreshTick} />}
        </ErrorBoundary>
      </main>

      {/* Log panel */}
      <LogPanel entries={logEntries} onClear={() => setLogEntries([])} />
    </div>
  )
}

/** Resolves authentication before mounting MainApp (which uses the WebSocket
 *  hook), so React hooks never run conditionally. While auth is null we're
 *  still loading; on a transient error we fall back to a permissive status so
 *  a hiccup can't lock the user out. */
export default function App() {
  const [auth, setAuth] = useState<AuthStatus | null>(null)

  useEffect(() => {
    api.getAuthStatus()
      .then(setAuth)
      .catch(() => setAuth({ authEnabled: false, authenticated: true }))
  }, [])

  const handleLogout = useCallback(() => {
    api.logout()
      .catch(() => { /* clearing the cookie is best-effort */ })
      .finally(() => setAuth({ authEnabled: true, authenticated: false }))
  }, [])

  if (auth === null) {
    return (
      <div className="login-shell" role="status" aria-live="polite">
        <div className="login-loading">Loading…</div>
      </div>
    )
  }

  if (auth.authEnabled && !auth.authenticated) {
    return <Login onLoggedIn={setAuth} />
  }

  return <MainApp auth={auth} onLogout={handleLogout} />
}
