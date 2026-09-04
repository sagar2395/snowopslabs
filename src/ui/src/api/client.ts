import type {
  StatusResponse,
  AppInfo,
  AppDetail,
  PlatformProvidersMap,
  PlatformComponentDetail,
  DashboardURL,
  Scenario,
  ScenarioVerifyResult,
  Runtime,
  ActionAccepted,
  JobInfo,
  LearnPathSummary,
  LearnPath,
  LearnProgress,
  ChallengeSummary,
  ChallengeStatus,
  ChallengeRunRecord,
  IncidentList,
  IncidentStatus,
  IncidentHint,
  IncidentHistoryRecord,
  Fault,
  ResultRecord,
  LeaderboardEntry,
  AuthStatus,
  RunSummary,
  RunDetail,
  RunLogs,
} from '../types'

const BASE = '/api/v2'

/** Read (GET) requests should answer fast; the server caps k8s calls at 10 s. */
const GET_TIMEOUT_MS = 15_000
/** Action (POST) requests return 202 immediately; allow some slack. */
const POST_TIMEOUT_MS = 30_000

/** Generic fetch wrapper — throws Error with a useful message on every
 *  failure mode: network down, timeout, HTTP error body, malformed JSON. */
async function req<T>(path: string, opts?: RequestInit, timeoutMs = GET_TIMEOUT_MS): Promise<T> {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)

  let res: Response
  try {
    res = await fetch(BASE + path, { ...opts, signal: ctrl.signal })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') {
      throw new Error(`Request timed out after ${Math.round(timeoutMs / 1000)}s — the server may be busy or stuck`)
    }
    // fetch() rejects with TypeError when the server is unreachable
    throw new Error('Cannot reach the labctl server — is `labctl ui` still running?')
  } finally {
    clearTimeout(timer)
  }

  if (!res.ok) {
    let msg = `HTTP ${res.status} ${res.statusText}`.trim()
    try {
      // Errors are application/problem+json (RFC 7807). `detail` carries the
      // human-readable cause; `title` is the fallback for the error class.
      const body = await res.json() as { detail?: string; title?: string }
      if (body.detail) msg = body.detail
      else if (body.title) msg = body.title
    } catch { /* non-JSON error body — keep the HTTP status message */ }
    throw new Error(msg)
  }

  try {
    return await res.json() as T
  } catch {
    throw new Error('Server returned an invalid response (not JSON)')
  }
}

/** Largest page the server will serve; asking for it keeps a normal-sized
 *  collection to a single round trip. */
const PAGE_LIMIT = 200
/** Refuse to page forever if the server keeps handing back a cursor. */
const MAX_PAGES = 50

/** Reads a paginated collection whole. List endpoints answer with
 *  `{items, nextCursor}`; views here render a full list, so follow the cursor
 *  until it comes back empty rather than silently truncating at one page. */
async function reqList<T>(path: string, params?: Record<string, string>): Promise<T[]> {
  const out: T[] = []
  let cursor = ''
  for (let page = 0; page < MAX_PAGES; page++) {
    const q = new URLSearchParams({ ...params, limit: String(PAGE_LIMIT) })
    if (cursor) q.set('cursor', cursor)
    const res = await req<{ items: T[]; nextCursor?: string }>(`${path}?${q}`)
    if (!Array.isArray(res?.items)) {
      throw new Error(`Server returned an unexpected shape for ${path} (no items array)`)
    }
    out.push(...res.items)
    if (!res.nextCursor) return out
    cursor = res.nextCursor
  }
  return out
}

function post(path: string, body?: unknown) {
  const opts: RequestInit = { method: 'POST' }
  if (body !== undefined) {
    opts.body = JSON.stringify(body)
    opts.headers = { 'Content-Type': 'application/json' }
  }
  return req<ActionAccepted>(path, opts, POST_TIMEOUT_MS)
}

export const api = {
  // ── Auth ────────────────────────────────────────────────────────────────
  getAuthStatus: ()                               => req<AuthStatus>('/auth/me'),
  login:         (username: string, password: string) => req<AuthStatus>('/auth/login', { method: 'POST', body: JSON.stringify({ username, password }), headers: { 'Content-Type': 'application/json' } }, POST_TIMEOUT_MS),
  logout:        ()                               => req<{ ok: boolean }>('/auth/logout', { method: 'POST' }, POST_TIMEOUT_MS),

  // ── Status ──────────────────────────────────────────────────────────────
  getStatus:    ()           => req<StatusResponse>('/status'),
  getDashboards: ()          => req<DashboardURL[]>('/dashboards'),
  getJobs:      ()           => req<JobInfo[]>('/jobs'),

  // ── Apps ────────────────────────────────────────────────────────────────
  listApps:   ()             => req<AppInfo[]>('/apps'),
  getAppDetail: (name: string) => req<AppDetail>(`/apps/${enc(name)}/detail`),
  buildApp:   (name: string) => post(`/apps/${enc(name)}/build`),
  deployApp:  (name: string) => post(`/apps/${enc(name)}/deploy`),
  destroyApp: (name: string) => post(`/apps/${enc(name)}/destroy`),

  // ── Platform ─────────────────────────────────────────────────────────────
  getPlatform:   ()                          => req<PlatformProvidersMap>('/platform'),
  platformUp:    ()                          => post('/platform/up'),
  platformDown:  ()                          => post('/platform/down'),
  // Tears the lab back to post-init (deactivates scenarios/incidents, destroys
  // apps, uninstalls non-baseline platform). Destructive → confirm=true required.
  resetLab:      ()                          => post('/lab/reset?confirm=true'),
  componentUp:   (cat: string, name: string) => post(`/platform/component/${enc(catSegment(cat))}/${enc(name)}/up`),
  componentDown: (cat: string, name: string) => post(`/platform/component/${enc(catSegment(cat))}/${enc(name)}/down`),
  getComponent:  (cat: string, name: string) => req<PlatformComponentDetail>(`/platform/component/${enc(catSegment(cat))}/${enc(name)}`),

  // ── Scenarios ─────────────────────────────────────────────────────────────
  listScenarios: ()             => reqList<Scenario>('/scenarios'),
  getScenario:   (name: string) => req<Scenario>(`/scenarios/${enc(name)}`),
  scenarioUp:    (name: string, params?: Record<string, string>) =>
    post(`/scenarios/${enc(name)}/up`, params && Object.keys(params).length ? { params } : undefined),
  scenarioDown:  (name: string) => post(`/scenarios/${enc(name)}/down`),
  // Verify is synchronous: it returns the per-check results directly (not a job),
  // so it parses the ScenarioVerifyResult body rather than an ActionAccepted.
  scenarioVerify: (name: string) => req<ScenarioVerifyResult>(`/scenarios/${enc(name)}/verify`, { method: 'POST' }, POST_TIMEOUT_MS),

  // ── Runtimes ──────────────────────────────────────────────────────────────
  listRuntimes:     ()             => req<Runtime[]>('/runtimes'),
  activateRuntime:  (name: string) => post(`/runtimes/${enc(name)}/activate`),
  deactivateRuntime:(name: string) => post(`/runtimes/${enc(name)}/deactivate`),

  // ── Learn ─────────────────────────────────────────────────────────────────
  listLearnPaths:  ()             => reqList<LearnPathSummary>('/learn/paths'),
  getLearnPath:    (name: string) => req<LearnPath>(`/learn/paths/${enc(name)}`),
  getLearnProgress:(name: string) => req<LearnProgress>(`/learn/paths/${enc(name)}/progress`),
  startLearnPath:  (name: string) => req<LearnProgress>(`/learn/paths/${enc(name)}/start`, { method: 'POST' }, POST_TIMEOUT_MS),
  resetLearnPath:  (name: string) => req<LearnProgress>(`/learn/paths/${enc(name)}/reset`, { method: 'POST' }, POST_TIMEOUT_MS),
  completeLearnModule: (name: string, moduleIdx: number) =>
    req<LearnProgress>(`/learn/paths/${enc(name)}/complete`, {
      method: 'POST',
      body: JSON.stringify({ moduleIdx }),
      headers: { 'Content-Type': 'application/json' },
    }, POST_TIMEOUT_MS),

  // ── Incidents ─────────────────────────────────────────────────────────────
  // inject/resolve/hint are synchronous (not durable jobs): they return the
  // final state directly, so callers manage their own busy flags.
  listIncidents:      ()             => req<IncidentList>('/incidents'),
  getIncidentStatus:  ()             => req<IncidentStatus>('/incidents/status'),
  getIncidentHistory: ()             => req<IncidentHistoryRecord[]>('/incidents/history'),
  injectIncident:     (name: string) => req<{ status: string; silent: boolean; fault?: Fault }>(`/incidents/${enc(name)}/inject`, { method: 'POST' }, POST_TIMEOUT_MS),
  injectRandomIncident: ()           => req<{ status: string; silent: boolean; fault?: Fault }>('/incidents/inject-random', { method: 'POST' }, POST_TIMEOUT_MS),
  resolveIncident:    ()             => req<{ status: string; fault: string }>('/incidents/resolve', { method: 'POST' }, POST_TIMEOUT_MS),
  nextIncidentHint:   ()             => req<IncidentHint>('/incidents/hint', { method: 'POST' }, POST_TIMEOUT_MS),

  // ── Challenges ────────────────────────────────────────────────────────────
  listChallenges:     ()             => reqList<ChallengeSummary>('/challenges'),
  getChallengeStatus: ()             => req<ChallengeStatus>('/challenges/status'),
  getChallengeHistory:()             => req<ChallengeRunRecord[]>('/challenges/history'),

  // ── Results ───────────────────────────────────────────────────────────────
  getResults:      ()             => req<ResultRecord[]>('/results'),
  getResultsByKind:(kind: string) => req<ResultRecord[]>(`/results/${enc(kind)}`),

  // ── Leaderboard ─────────────────────────────────────────────────────────────
  getLeaderboard:  ()             => req<LeaderboardEntry[]>('/leaderboard'),

  // ── Traffic generator ───────────────────────────────────────────────────────
  getTraffic:   ()                        => req<{ profiles: string[] }>('/traffic'),
  startTraffic: (opts: TrafficOptions)    => req<ActionAccepted>('/traffic/start',
    { method: 'POST', body: JSON.stringify(opts), headers: { 'Content-Type': 'application/json' } }, POST_TIMEOUT_MS),
  stopTraffic:  ()                        => post('/traffic/stop'),

  // ── Durable runs (run console) ──────────────────────────────────────────────
  listRuns:  (status?: string)            => reqList<RunSummary>('/runs', status ? { status } : undefined),
  getRun:    (id: string)                 => req<RunDetail>(`/runs/${enc(id)}`),
  getRunLogs:(id: string, after = 0)      => req<RunLogs>(`/runs/${enc(id)}/logs?after=${after}`),
  cancelRun: (id: string)                 => req<{ status: string; runId: string }>(`/runs/${enc(id)}/cancel`, { method: 'POST' }, POST_TIMEOUT_MS),

}

export interface TrafficOptions {
  profile: string
  rps: number
  duration?: string
  target?: string
}

function enc(s: string) { return encodeURIComponent(s) }

/** Nested registry categories ("monitoring/metrics") can't travel as one URL
 *  path segment — send the last segment; the server resolves it back. */
function catSegment(cat: string) {
  const parts = cat.split('/')
  return parts[parts.length - 1]
}
