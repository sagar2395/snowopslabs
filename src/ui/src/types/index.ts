// ── Auth ──────────────────────────────────────────────────────────────────────

export type AuthRole = 'operator' | 'participant'

/** GET /api/auth/me (and login) response. When authEnabled is false the UI
 *  renders normally with no login screen (authenticated is always true). */
export interface AuthStatus {
  authEnabled: boolean
  authenticated: boolean
  user?: string
  role?: AuthRole
}

// ── Cluster & Status ─────────────────────────────────────────────────────────

export interface ClusterInfo {
  context: string
  server: string
  k8sVersion: string
  nodeCount: number
  connected: boolean
}

export interface PlatformComponent {
  provider: string
  active: boolean
}

export interface PlatformStatus {
  ingress?: PlatformComponent
  metrics?: PlatformComponent
  logging?: PlatformComponent
  tracing?: PlatformComponent
  gitops?: PlatformComponent
  chaos?: PlatformComponent
  policy?: PlatformComponent
  secrets?: PlatformComponent
  [key: string]: PlatformComponent | undefined
}

/** One provider entry from GET /api/platform — the full registry view
 *  (every provider per category, e.g. ingress → traefik AND nginx). */
export interface PlatformProviderEntry {
  name: string
  category: string
  installed: boolean
  /** True when the category's providers are mutually exclusive (pick one, e.g.
   *  ingress/mesh). False/absent means they're complementary and installed
   *  together (e.g. secrets: vault + external-secrets). */
  exclusive?: boolean
}

/** GET /api/platform response: category key → providers.
 *  Category keys may be nested ("monitoring/metrics"). */
export type PlatformProvidersMap = Record<string, PlatformProviderEntry[]>

export interface AppInfo {
  name: string
  buildStrategy: string
  deployStrategy: string
  deployed: boolean
  replicas?: string
  ready?: string
  namespace?: string
  /** Ingress URL (http://<app>.<domainSuffix>), set once deployed. */
  url?: string
}

export interface StatusResponse {
  cluster: ClusterInfo | null
  platform: PlatformStatus
  apps: AppInfo[] | null
  domainSuffix: string
}

// ── Scenarios ────────────────────────────────────────────────────────────────

export interface ScenarioPrerequisites {
  platform?: string[]
  apps?: string[]
}

export interface ExploreURL {
  label: string
  url: string
}

export interface ExploreCommand {
  label: string
  command: string
}

export interface Explore {
  urls?: ExploreURL[]
  commands?: ExploreCommand[]
  tips?: string[]
}

export interface ScenarioComponent {
  name: string
  type: string
  chart?: string
  namespace?: string
  path?: string
  script?: string
  version?: string
}

export interface Scenario {
  name: string
  displayName: string
  description: string
  category: string
  active: boolean
  runtimes?: string[]
  prerequisites?: ScenarioPrerequisites
  components?: ScenarioComponent[]
  explore?: Explore
}

// ── Dashboards & Runtimes ────────────────────────────────────────────────────

export interface DashboardURL {
  name: string
  label: string
  url: string
  available: boolean
  category: string
}

export interface Runtime {
  name?: string
  profile?: string
  active?: boolean
  current?: boolean
}

// ── Async actions / jobs ─────────────────────────────────────────────────────

/** 202 response body for every POST action endpoint. */
export interface ActionAccepted {
  jobId: string
  status: string
}

/** One entry from GET /api/jobs — recorded action history, newest first. */
export interface JobInfo {
  id: string
  action: string
  status: 'running' | 'succeeded' | 'failed'
  error?: string
  startedAt: string
  endedAt?: string
}

// ── Durable runs (run console) ───────────────────────────────────────────────

export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'timed_out'

/** One recorded run from GET /api/runs (the durable run engine's history). */
export interface RunSummary {
  id: string
  kind: string
  target?: string
  status: RunStatus
  actor?: string
  queuedAt: string
  startedAt?: string
  endedAt?: string
  durationMs?: number
  exitCode?: number
  error?: string
}

export interface RunStep {
  index: number
  name: string
  status: 'running' | 'succeeded' | 'failed'
  startedAt: string
  endedAt?: string
}

/** GET /api/runs/{id} — a run plus its step timeline. */
export interface RunDetail extends RunSummary {
  steps: RunStep[]
}

export interface RunLogLine {
  seq: number
  at: string
  stream: 'stdout' | 'stderr' | 'system'
  text: string
}

/** GET /api/runs/{id}/logs?after= — a cursor-forward slice of the transcript. */
export interface RunLogs {
  runId: string
  status: RunStatus
  lines: RunLogLine[]
  nextAfter: number
  done: boolean
}

// ── WebSocket events ─────────────────────────────────────────────────────────

export type ActionEventType = 'action_start' | 'action_output' | 'action_end' | 'action_error'

export interface ActionEvent {
  id?: string
  type: ActionEventType
  action?: string
  command?: string
  output?: string
  stream?: 'stdout' | 'stderr'
  exitCode?: number
  error?: string
  timestamp?: string
}

export interface WSMessage {
  type: 'status' | 'action'
  data: unknown
}

// ── Log entries ───────────────────────────────────────────────────────────────

export type LogLevel = 'cmd' | 'output' | 'stderr' | 'success' | 'error'

export interface LogEntry {
  id: number
  ts: string
  level: LogLevel
  text: string
}

// ── Notifications ─────────────────────────────────────────────────────────────

export type NotifLevel = 'info' | 'success' | 'error'

export interface Notification {
  id: number
  level: NotifLevel
  title: string
  detail?: string
}

export type NotifyFn = (level: NotifLevel, title: string, detail?: string) => void

// ── Learn ─────────────────────────────────────────────────────────────────────

export interface LearnPathSummary {
  name: string
  displayName: string
  description: string
  tags: string[]
  estimatedMinutes: number
  moduleCount: number
  completedCount: number
}

export interface LearnModuleAction {
  type: string
  ref: string
}

export interface LearnModule {
  name: string
  displayName: string
  hasIntro: boolean
  action: LearnModuleAction
  completed: boolean
}

export interface LearnPath {
  name: string
  displayName: string
  description: string
  tags: string[]
  modules: LearnModule[]
}

export interface LearnProgress {
  path: string
  started: boolean
  completed?: number[]
  total?: number
  nextIdx?: number
  startedAt?: string
  updatedAt?: string
}

// ── Challenges ────────────────────────────────────────────────────────────────

export interface ChallengeSummary {
  name: string
  displayName: string
  description: string
  category: string
  parTime: string
}

export interface ChallengeStatus {
  active: boolean
  challenge?: string
  startedAt?: string
  hintsUsed?: number
  elapsed?: string
}

export interface ChallengeRunRecord {
  challenge: string
  startedAt: string
  finishedAt: string
  elapsedSeconds: number
  hintsUsed: number
  checksPassed: number
  checksTotal: number
  score: number
  outcome: string
}

// ── Results ───────────────────────────────────────────────────────────────────

export interface ResultRecord {
  kind: string
  name: string
  user?: string
  startedAt: string
  endedAt: string
  elapsedSeconds: number
  score: number
  outcome: string
  hintsUsed?: number
  meta?: Record<string, unknown>
}

// ── Incidents ─────────────────────────────────────────────────────────────────

/** A doc/tool reference or an applyable manifest snippet a fault can surface
 *  (M2). Loosely typed — the view only reads label/url/description. */
export interface ContentReference { label: string; url: string; note?: string }
export interface ContentSnippet { label: string; description?: string; yaml?: string; path?: string }

/** One fault from the incident catalog (GET /api/incidents → faults[]). */
export interface Fault {
  name: string
  displayName: string
  description: string
  verified: boolean
  category: string          // workload | network | resources | storage | config
  severity: string          // low | medium | high
  expectAlert?: string
  prerequisites?: { platform?: string[]; apps?: string[] }
  references?: ContentReference[]
  snippets?: ContentSnippet[]
}

/** The live active-incident record (nil when nothing is injected). */
export interface IncidentActive {
  fault: string
  injectedAt: string
  silent: boolean
  hintsRevealed: number
  firstCheckedAt?: string
}

/** A detection-check outcome (pkg/checks.Result). Pass ⇔ the fault is resolved. */
export interface IncidentCheck {
  name: string
  type?: string
  pass: boolean
  got?: string
  want?: string
  explanation?: string
  error?: string
}

/** GET /api/incidents — catalog plus the current active state. */
export interface IncidentList {
  faults: Fault[]
  active: IncidentActive | null
}

/** GET /api/incidents/status — active incident + live detection check.
 *  `active` is null when nothing is injected. */
export interface IncidentStatus {
  active: IncidentActive | null
  fault?: Fault | null
  check?: IncidentCheck
  resolved?: boolean
}

/** One progressively-revealed hint (POST /api/incidents/hint). */
export interface IncidentHint { index: number; total: number; text: string }

/** One past incident run (GET /api/incidents/history). */
export interface IncidentHistoryRecord {
  fault: string
  injectedAt: string
  resolvedAt: string
  resolveSeconds: number
  hintsUsed: number
  detectSeconds?: number
  severity?: string
  category?: string
  resolvedBy?: string
}

// ── Leaderboard ─────────────────────────────────────────────────────────────

export interface LeaderboardEntry {
  user: string
  totalScore: number
  challengesCompleted: number
  incidentsResolved: number
  modulesCompleted: number
  hintsUsed: number
  avgMttrSeconds: number
  runs: number
}
