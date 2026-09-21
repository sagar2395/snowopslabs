// ── Auth ──────────────────────────────────────────────────────────────────────

export type AuthRole = 'operator' | 'participant'

/** GET /api/v2/auth/me (and login) response. When authEnabled is false the UI
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

/** One provider entry from GET /api/v2/platform — the full registry view
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

/** GET /api/v2/platform response: category key → providers.
 *  Category keys may be nested ("monitoring/metrics"). */
export type PlatformProvidersMap = Record<string, PlatformProviderEntry[]>

/** A compact scenario pointer (used by the "used in scenarios" list). */
export interface ScenarioRef {
  name: string
  displayName: string
}

/** GET /api/v2/platform/component/:cat/:name — the per-tool details page. */
export interface PlatformComponentDetail {
  category: string
  name: string
  namespace: string
  installed: boolean
  description: string
  provides: string[]
  ports: string[]
  dependencies: string[]
  resources: string[]
  chart: string
  /** The helm/kubectl commands install.sh actually runs, for "how it's set up". */
  installCommands: string[]
  /** Legend for the shell variables appearing in installCommands. */
  installVars?: InstallVar[]
  usedInScenarios: ScenarioRef[]
}

/** One shell variable referenced by a component's install commands. */
export interface InstallVar {
  name: string
  value: string
  description: string
}

/** One source file surfaced on an app's details page. */
export interface AppFileRef {
  path: string
  content: string
  truncated?: boolean
}

/** GET /api/v2/apps/:name/detail — how an app is built and deployed. */
export interface AppDetail {
  name: string
  description: string
  tech: string[]
  buildStrategy: string
  deployStrategy: string
  namespace: string
  dockerfile?: AppFileRef
  chartYaml?: AppFileRef
  valuesFile?: AppFileRef
  helmChartPath?: string
  templates?: string[]
}

/** Live HorizontalPodAutoscaler state for an app (present ⇔ an HPA, including a
 *  KEDA-managed one, targets it). Metric* strings are pre-rendered by the API,
 *  e.g. current "27467m" vs target "25", or "92%" vs "80%" for a CPU HPA. */
export interface HPAStatus {
  present: boolean
  name: string
  minReplicas: number
  maxReplicas: number
  currentReplicas: number
  desiredReplicas: number
  metricName?: string
  metricCurrent?: string
  metricTarget?: string
}

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
  /** In-cluster base URL from the app's declared contract — the traffic target. */
  serviceUrl?: string
  /** Capabilities the app declares (ADR-0014). */
  capabilities?: string[]
  /** Autoscaler state, present only when an HPA targets the app. */
  hpa?: HPAStatus
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
  /** Capabilities the bound workload must declare, e.g. prometheus-metrics. */
  capabilities?: string[]
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

/** A user-tunable knob a scenario exposes at activation time (e.g. an
 *  autoscaler's min/max replicas and threshold). Substituted into the scenario's
 *  manifests as a {{.name}} template variable. */
export interface ScenarioParameter {
  name: string
  displayName?: string
  description?: string
  default: string
  type?: 'int' | 'string'
  min?: number
  max?: number
  notGreaterThan?: string
}

/** A machine-verifiable check declared by a scenario (pkg/checks.Check). The
 *  assertion is either a kubectl resource/jsonpath or a promql query, compared
 *  to `value` with `operator`. */
export interface ScenarioCheck {
  name: string
  type?: string
  resource?: string
  namespace?: string
  jsonpath?: string
  query?: string
  operator?: string
  value?: string
}

export interface ScenarioStage {
  name: string
  description?: string
  components?: ScenarioComponent[]
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
  stages?: ScenarioStage[]
  explore?: Explore
  parameters?: ScenarioParameter[]
  objectives?: string[]
  checks?: ScenarioCheck[]
  snippets?: ContentSnippet[]
  /** Apps the scenario names literally — the only app prerequisites a user has
   *  to satisfy. An app that came from the binding is in `workload`, not here. */
  pinnedApps?: string[]
  /** The workload this scenario will run (or is running) against. */
  workload?: WorkloadBinding
  /** The app an active scenario was activated against (catalog rows only). */
  app?: string
}

/** One check outcome from POST /api/v2/scenarios/{name}/verify (pkg/checks.Result).
 *  Pass ⇔ the asserted post-state holds. */
export interface ScenarioCheckResult {
  name: string
  type?: string
  pass: boolean
  got?: string
  want?: string
  explanation?: string
  error?: string
  durationMs?: number
}

/** POST /api/v2/scenarios/{name}/verify response. */
export interface ScenarioVerifyResult {
  scenario: string
  passed: boolean
  results: ScenarioCheckResult[]
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

/** One entry from GET /api/v2/jobs — recorded action history, newest first. */
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

/** One recorded run from GET /api/v2/runs (the durable run engine's history). */
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

/** GET /api/v2/runs/{id} — a run plus its step timeline. */
export interface RunDetail extends RunSummary {
  steps: RunStep[]
}

export interface RunLogLine {
  seq: number
  at: string
  stream: 'stdout' | 'stderr' | 'system'
  text: string
}

/** GET /api/v2/runs/{id}/logs?after= — a cursor-forward slice of the transcript. */
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

/** A doc/tool link a scenario or fault surfaces. */
export interface ContentReference { label: string; url: string; note?: string }

/** A file or fragment a scenario or fault shows the learner. `yaml` is the body
 *  as the server renders it: read from `path`, trimmed of long comment blocks and
 *  resolved for the bound workload. */
export interface ContentSnippet {
  label: string
  description?: string
  yaml?: string
  path?: string
  /** Nothing installs an exercise snippet: applying it is the learner's task. */
  exercise?: boolean
  /** Ready-to-run command for an exercise, when it has one. */
  applyCommand?: string
}

/** One fault from the incident catalog (GET /api/v2/incidents → faults[]). */
export interface Fault {
  name: string
  displayName: string
  description: string
  verified: boolean
  category: string          // workload | network | resources | storage | config
  severity: string          // low | medium | high
  expectAlert?: string
  prerequisites?: { platform?: string[]; apps?: string[]; capabilities?: string[] }
  references?: ContentReference[]
  snippets?: ContentSnippet[]
  /** Apps the fault names literally — the only app prerequisites a user has to
   *  satisfy. An app that came from the binding is in `workload`, not here. */
  pinnedApps?: string[]
  /** The workload this fault will be (or was) injected into. */
  workload?: WorkloadBinding
}

/** The application a scenario or fault is bound to (ADR-0014). Content names the
 *  binding rather than an app, so the same content runs against any of them. */
export interface WorkloadBinding {
  app: string
  namespace: string
  service: string
  port: string
  metric: string
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

/** GET /api/v2/incidents — catalog plus the current active state. */
export interface IncidentList {
  faults: Fault[]
  active: IncidentActive | null
}

/** GET /api/v2/incidents/status — active incident + live detection check.
 *  `active` is null when nothing is injected. */
export interface IncidentStatus {
  active: IncidentActive | null
  fault?: Fault | null
  check?: IncidentCheck
  resolved?: boolean
}

/** One progressively-revealed hint (POST /api/v2/incidents/hint). */
export interface IncidentHint { index: number; total: number; text: string }

/** One past incident run (GET /api/v2/incidents/history). */
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

// ── Comparisons (ADR-0014 §6) ───────────────────────────────────────────────

/** One metric's definition, sent with the data so the client never hardcodes
 *  which way is better for a given key. */
export interface ComparisonMetric {
  key: string
  label: string
  unit: string
  lowerBetter: boolean
  /** True when neither direction is better (e.g. replica count). */
  neutral: boolean
  /** Multiplier from the raw Prometheus value into `unit`. */
  scale: number
  digits: number
}

/** One workload's numbers. A metric with no series is absent from `values`
 *  rather than zero — "used none" and "not measurable here" differ. */
export interface ComparisonMeasurement {
  app: string
  values: Record<string, number>
}

export interface Comparison {
  id: string
  scenario: string
  apps: string[]
  profile: string
  rps: number
  /** Load time excluded from the measurement, so runtime warm-up is not compared. */
  warmupSeconds: number
  windowSeconds: number
  startedAt: string
  metrics: ComparisonMetric[]
  measurements: ComparisonMeasurement[]
}

