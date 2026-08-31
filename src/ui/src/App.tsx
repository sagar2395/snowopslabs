import { useState, useCallback, useRef, useEffect, Component, type ReactNode } from 'react'
import {
  Routes, Route, Navigate, NavLink, Outlet,
  useOutletContext, useNavigate, useLocation,
} from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { useWebSocket, type WSStatus } from './hooks/useWebSocket'
import type { ActionEvent, AuthStatus, ClusterInfo, LogEntry, Notification, NotifLevel } from './types'
import { NotificationList } from './components/Notification'
import { ThemeToggle } from './components/ThemeToggle'
import { CommandPalette, type Command } from './components/CommandPalette'
import { Login } from './components/Login'
import { LogPanel } from './components/LogPanel'
import { ConfirmDialog, type ConfirmRequest } from './components/ConfirmDialog'
import { Icon, type IconName } from './components/Icon'
import { Dashboard } from './views/Dashboard'
import { Scenarios } from './views/Scenarios'
import { Incidents } from './views/Incidents'
import { Platform } from './views/Platform'
import { Apps } from './views/Apps'
import { Learn } from './views/Learn'
import { Challenges } from './views/Challenges'
import { Results } from './views/Results'
import { Leaderboard } from './views/Leaderboard'
import { Traffic } from './views/Traffic'
import { Runs } from './views/Runs'
import { api } from './api/client'
import { qk } from './lib/queryClient'
import { completeJob, reconcileJobs, hasTrackedJobs, trackJob } from './lib/jobs'

// Each section is a real, deep-linkable route. `path` is the URL segment; the
// nav renders in `NAV_GROUPS` order and arrow keys cycle the flattened list.
type NavItem = { path: string; label: string; icon: IconName }
type NavGroup = { label: string; items: NavItem[] }

// Grouped so eleven sections read as three intents rather than one long strip.
const NAV_GROUPS: NavGroup[] = [
  {
    label: 'Operate',
    items: [
      { path: 'dashboard', label: 'Dashboard', icon: 'dashboard' },
      { path: 'platform',  label: 'Platform',  icon: 'platform' },
      { path: 'apps',      label: 'Apps',      icon: 'apps' },
      { path: 'runs',      label: 'Runs',      icon: 'runs' },
      { path: 'traffic',   label: 'Traffic',   icon: 'traffic' },
    ],
  },
  {
    label: 'Practice',
    items: [
      { path: 'scenarios',  label: 'Scenarios',  icon: 'scenarios' },
      { path: 'incidents',  label: 'Incidents',  icon: 'incidents' },
      { path: 'learn',      label: 'Learn',      icon: 'learn' },
      { path: 'challenges', label: 'Challenges', icon: 'challenges' },
    ],
  },
  {
    label: 'Analyze',
    items: [
      { path: 'results',     label: 'Results',     icon: 'results' },
      { path: 'leaderboard', label: 'Leaderboard', icon: 'leaderboard' },
    ],
  },
]

// Flattened, in render order — used for arrow-key nav, the command palette, and
// resolving the current page's title.
const NAV: NavItem[] = NAV_GROUPS.flatMap(g => g.items)

// Shared state the layout owns and every view reads, delivered through the
// router Outlet so views stay plain components (no prop drilling through routes).
export type AppOutletContext = {
  notify: (level: NotifLevel, title: string, detail?: string) => void
  requestConfirm: (req: ConfirmRequest) => void
  liveCluster: ClusterInfo | null
  lastStatusAt: number | null
}

/** Views pull the shared app state from the router outlet. */
export function useApp(): AppOutletContext {
  return useOutletContext<AppOutletContext>()
}

let notifSeq = 0
let logSeq = 0

function nowHMS() {
  return new Date().toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

/** Catches render-time crashes in a view so one bad payload can't blank the
 *  whole UI — shows a reset affordance instead. Keyed by route so navigating
 *  away from a crashed view clears the error. */
class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) { return { error } }
  render() {
    if (this.state.error) {
      return (
        <div className="error-state" role="alert">
          <div className="error-state-title"><Icon name="alert-triangle" /> This view crashed</div>
          <div className="error-state-message">{this.state.error.message}</div>
          <button className="btn btn-sm" onClick={() => this.setState({ error: null })}>Reload view</button>
        </div>
      )
    }
    return this.props.children
  }
}

function AppLayout({ auth, onLogout }: { auth: AuthStatus; onLogout: () => void }) {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [wsStatus, setWsStatus] = useState<WSStatus>('connecting')
  const [notifications, setNotifications] = useState<Notification[]>([])
  const [logEntries, setLogEntries] = useState<LogEntry[]>([])
  const [runtimes, setRuntimes] = useState<string[]>([])
  const [activeRuntime, setActiveRuntime] = useState('')
  const [runtimeBusy, setRuntimeBusy] = useState(false)
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)
  const [liveCluster, setLiveCluster] = useState<ClusterInfo | null>(null)
  const [lastStatusAt, setLastStatusAt] = useState<number | null>(null)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [drawerOpen, setDrawerOpen] = useState(false)

  const notify = useCallback((level: NotifLevel, title: string, detail?: string) => {
    const id = ++notifSeq
    setNotifications(prev => [...prev, { id, level, title, detail }])
    // Errors linger longer (they matter more) but still auto-dismiss.
    setTimeout(() => dismiss(id), level === 'error' ? 18000 : 8000)
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

  // Debounced cache invalidation — coalesces bursts of action_end events into
  // one refetch. Every mounted view's useQuery reruns.
  const bumpTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const bumpRefresh = useCallback(() => {
    if (bumpTimer.current) return
    bumpTimer.current = setTimeout(() => {
      bumpTimer.current = null
      queryClient.invalidateQueries()
    }, 1200)
  }, [queryClient])
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

  // The live status frame (pushed every 5 s) carries no per-component state, but
  // its signature — connection, context, k8s version, node count — shifts when
  // the cluster is torn down, reset, or the runtime is switched *out of band*
  // (e.g. from the CLI). When it does, the platform/apps/status the cache holds
  // may be stale (a torn-down cluster still shows components as "Installed"), so
  // refetch the cluster-derived views. The first frame just seeds the baseline.
  const prevClusterSig = useRef<string | null>(null)
  const onClusterStatus = useCallback((info: ClusterInfo | null) => {
    setLiveCluster(info)
    setLastStatusAt(Date.now())
    const sig = info
      ? `${info.connected ? 1 : 0}|${info.context}|${info.k8sVersion}|${info.nodeCount}`
      : 'none'
    if (prevClusterSig.current !== null && prevClusterSig.current !== sig) {
      queryClient.invalidateQueries({ queryKey: qk.platform })
      queryClient.invalidateQueries({ queryKey: qk.status })
      queryClient.invalidateQueries({ queryKey: qk.dashboards })
    }
    prevClusterSig.current = sig
  }, [queryClient])

  // Detect disconnected → connected transitions to re-sync missed events.
  const prevWsStatus = useRef<WSStatus>('connecting')
  const onStatusChange = useCallback((s: WSStatus) => {
    const prev = prevWsStatus.current
    prevWsStatus.current = s
    setWsStatus(s)
    if (s === 'connected' && prev === 'disconnected') {
      notify('info', 'Reconnected', 'Live updates restored — refreshing data.')
      if (hasTrackedJobs()) {
        api.getJobs().then(reconcileJobs).catch(() => { /* next event will settle them */ })
      }
      queryClient.invalidateQueries()
    }
  }, [notify, queryClient])

  useWebSocket({ onActionEvent, onStatusChange, onClusterStatus })

  // Cmd/Ctrl-K toggles the command palette from anywhere in the app.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen(o => !o)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  // Close the mobile drawer whenever the route changes.
  useEffect(() => { setDrawerOpen(false) }, [location.pathname])

  // Esc closes the mobile drawer.
  useEffect(() => {
    if (!drawerOpen) return
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') setDrawerOpen(false) }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [drawerOpen])

  // Palette commands: jump to any section, plus a couple of quick actions.
  const commands: Command[] = [
    ...NAV.map(n => ({
      id: `go-${n.path}`,
      label: `Go to ${n.label}`,
      hint: `/${n.path}`,
      icon: n.icon,
      run: () => navigate(`/${n.path}`),
    })),
    { id: 'refresh', label: 'Refresh data', hint: 'reload all views', icon: 'refresh' as IconName, run: () => { queryClient.invalidateQueries() } },
  ]

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

  // Arrow-key navigation across the nav links, matching the section order.
  function onNavKeyDown(e: React.KeyboardEvent) {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    const current = location.pathname.replace(/^\//, '') || 'dashboard'
    const idx = NAV.findIndex(n => n.path === current)
    if (idx === -1) return
    e.preventDefault()
    const delta = e.key === 'ArrowDown' ? 1 : -1
    const next = NAV[(idx + delta + NAV.length) % NAV.length]
    navigate(`/${next.path}`)
    document.getElementById(`nav-${next.path}`)?.focus()
  }

  const connDot = wsStatus === 'connected' ? 'green' : wsStatus === 'connecting' ? 'yellow' : 'red'
  const connLabel = wsStatus === 'connected' ? 'Connected' : wsStatus === 'connecting' ? 'Connecting…' : 'Disconnected'

  const current = location.pathname.replace(/^\//, '') || 'dashboard'
  const pageTitle = NAV.find(n => n.path === current)?.label ?? 'SnowOps Labs'

  const ctx: AppOutletContext = { notify, requestConfirm: setConfirm, liveCluster, lastStatusAt }

  return (
    <div className="app-shell">
      <a href="#main-content" className="skip-link">Skip to main content</a>
      <NotificationList items={notifications} onDismiss={dismiss} />
      <ConfirmDialog request={confirm} onClose={() => setConfirm(null)} />
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} commands={commands} />

      {/* Sidebar — brand, grouped nav, and runtime/connection footer. On mobile
          it slides in as a drawer over the content. */}
      <aside className={`sidebar${drawerOpen ? ' is-open' : ''}`}>
        <div className="sidebar-brand">
          <span className="brand-mark" aria-hidden="true"><Icon name="snowflake" size={18} /></span>
          <span className="brand-name"><span className="accent">SnowOps</span> Labs</span>
        </div>

        <nav className="sidebar-nav" aria-label="Sections" onKeyDown={onNavKeyDown}>
          {NAV_GROUPS.map(group => (
            <div className="nav-group" key={group.label}>
              <div className="nav-group-label">{group.label}</div>
              {group.items.map(n => (
                <NavLink
                  key={n.path}
                  id={`nav-${n.path}`}
                  to={`/${n.path}`}
                  className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
                >
                  <Icon name={n.icon} size={18} className="nav-item-icon" />
                  {n.label}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>

        <div className="sidebar-footer">
          {runtimes.length > 0 && (
            <div className="runtime-select-wrap">
              <span className="runtime-select-label" id="runtime-label">Runtime</span>
              <select
                className="runtime-select"
                value={activeRuntime}
                disabled={runtimeBusy}
                aria-labelledby="runtime-label"
                onChange={e => handleRuntimeChange(e.target.value)}
              >
                {!activeRuntime && <option value="">select runtime…</option>}
                {runtimes.map(r => (
                  <option key={r} value={r}>{r}{r === activeRuntime ? ' (active)' : ''}</option>
                ))}
              </select>
            </div>
          )}
          <div className="conn-indicator" role="status" aria-live="polite">
            <span className={`dot dot-${connDot}`} aria-hidden="true" />
            <span>{connLabel}</span>
          </div>
        </div>
      </aside>

      {/* Drawer scrim (mobile only, shown via CSS when the drawer is open). */}
      {drawerOpen && (
        <button
          type="button"
          className="sidebar-scrim is-open"
          aria-label="Close navigation"
          onClick={() => setDrawerOpen(false)}
        />
      )}

      {/* Main column: sticky topbar + content. */}
      <div className="app-main-col">
        <header className="topbar">
          <button
            type="button"
            className="icon-btn-square"
            aria-label="Open navigation"
            aria-expanded={drawerOpen}
            onClick={() => setDrawerOpen(o => !o)}
          >
            <Icon name="menu" size={20} />
          </button>
          <h1 className="topbar-title">{pageTitle}</h1>
          <div className="topbar-spacer" />
          <div className="topbar-actions">
            <button
              type="button"
              className="cmdk-trigger"
              aria-label="Open command palette"
              aria-keyshortcuts="Meta+K Control+K"
              onClick={() => setPaletteOpen(true)}
            >
              <Icon name="command" size={14} />
              <span>Jump to</span>
              <span className="cmdk-kbd" aria-hidden="true">⌘K</span>
            </button>
            <ThemeToggle />
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

        {/* Non-blocking offline state — a live spinner makes clear the app is
            actively reconnecting (and recovers on its own; see onStatusChange). */}
        {wsStatus === 'disconnected' && (
          <div className="banner banner-warn banner-reconnecting" role="alert">
            <span className="banner-icon" aria-hidden="true" />
            <span className="banner-body">Live updates unavailable — reconnecting to the labctl server…</span>
          </div>
        )}

        <main className="main-content" id="main-content" tabIndex={-1}>
          <ErrorBoundary key={location.pathname}>
            <Outlet context={ctx} />
          </ErrorBoundary>
        </main>
      </div>

      <LogPanel entries={logEntries} onClear={() => setLogEntries([])} />
    </div>
  )
}

// Thin route elements: adapt the shared outlet context to each view's props.
function DashboardRoute() {
  const { notify, liveCluster, lastStatusAt, requestConfirm } = useApp()
  return <Dashboard notify={notify} liveCluster={liveCluster} lastStatusAt={lastStatusAt} requestConfirm={requestConfirm} />
}
function ScenariosRoute() {
  const { notify, requestConfirm } = useApp()
  return <Scenarios notify={notify} requestConfirm={requestConfirm} />
}
function IncidentsRoute() {
  const { notify, requestConfirm } = useApp()
  return <Incidents notify={notify} requestConfirm={requestConfirm} />
}
function PlatformRoute() {
  const { notify, requestConfirm } = useApp()
  return <Platform notify={notify} requestConfirm={requestConfirm} />
}
function AppsRoute() {
  const { notify, requestConfirm } = useApp()
  return <Apps notify={notify} requestConfirm={requestConfirm} />
}
function TrafficRoute() {
  const { notify } = useApp()
  return <Traffic notify={notify} />
}
function RunsRoute() {
  const { notify } = useApp()
  return <Runs notify={notify} />
}
function LearnRoute() {
  const { notify, requestConfirm } = useApp()
  return <Learn notify={notify} requestConfirm={requestConfirm} />
}
function ChallengesRoute() {
  const { notify, requestConfirm } = useApp()
  return <Challenges notify={notify} requestConfirm={requestConfirm} />
}
function ResultsRoute() {
  const { notify } = useApp()
  return <Results notify={notify} />
}
function LeaderboardRoute() {
  const { notify } = useApp()
  return <Leaderboard notify={notify} />
}

/** Resolves authentication before mounting the app (which uses the WebSocket
 *  hook), so React hooks never run conditionally. */
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

  return (
    <Routes>
      <Route element={<AppLayout auth={auth} onLogout={handleLogout} />}>
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="dashboard" element={<DashboardRoute />} />
        <Route path="scenarios" element={<ScenariosRoute />} />
        <Route path="incidents" element={<IncidentsRoute />} />
        <Route path="platform" element={<PlatformRoute />} />
        <Route path="apps" element={<AppsRoute />} />
        <Route path="runs" element={<RunsRoute />} />
        <Route path="traffic" element={<TrafficRoute />} />
        <Route path="learn" element={<LearnRoute />} />
        <Route path="challenges" element={<ChallengesRoute />} />
        <Route path="results" element={<ResultsRoute />} />
        <Route path="leaderboard" element={<LeaderboardRoute />} />
        {/* Unknown paths fall back to the dashboard rather than a blank screen. */}
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Route>
    </Routes>
  )
}
