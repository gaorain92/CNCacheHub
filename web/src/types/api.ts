// 后端响应类型定义
// 命名风格：与 Go 后端 JSON tag（camelCase）保持一致
// 字段尽量保持可选，避免在版本演进时阻塞前端

export interface HealthResponse {
  status: string
  uptime?: string
  db?: string
  version?: string
}

export interface VersionResponse {
  name: string
  version: string
  go: string
  commit: string
}

// ====================================================================
// Phase 1 — Docker pull-through cache
// ====================================================================

export interface Upstream {
  id: number
  name: string
  upstreamUrl: string
  mirrorPath: string
  enabled: boolean
}

export interface UpstreamsResponse {
  items: Upstream[]
  total: number
}

export interface DashboardSummary {
  cacheEntries: number
  cacheBytes: number
  cacheHits: number
  bypassedCount: number
  hitCount: number
  missCount: number
  requestCount24h: number
  errorCount24h: number
  bytesOut24h: number
  activeUpstreams: number
  generatedAt: number
}

export interface AccessLogItem {
  method: string
  path: string
  status: number
  durationMs: number
  cached: boolean
  bypassed: boolean
  bypassReason: string // PRD §9.6.4: 'size_limit' | 'disk_low' | ''
  clientIp: string
  bytes: number
  error: string
}

export interface AccessLogsResponse {
  items: AccessLogItem[]
  total: number
  page: number
  pageSize: number
}

// ============================================================================
// System Settings
// ============================================================================

export interface SystemSettings {
  smallVpsOpt: boolean
  reserveSpaceGb: number
  maxObjectSizeMb: number
  cacheTotalGb: number
  cleanupTriggerPct: number
  cleanupTargetPct: number
  updatedAt: number
}

export interface SettingsPatch {
  smallVpsOpt?: boolean
  reserveSpaceGb?: number
  maxObjectSizeMb?: number
  cacheTotalGb?: number
  cleanupTriggerPct?: number
  cleanupTargetPct?: number
}

// ============================================================================
// Registry Upstreams (PRD §9.2.2)
// ============================================================================

export interface Registry {
  id: number
  name: string
  upstreamUrl: string
  mirrorPath: string
  enabled: boolean
  createdAt: number
}

export interface RegistryPatch {
  enabled: boolean
}

export interface CacheEntry {
  id: number
  registry: string
  repository: string
  digest: string
  mediaType: string
  sizeBytes: number
  storagePath: string
  hitCount: number
  lastAccessAt: number
  createdAt: number
  bypassed: boolean
  bypassReason: string
}

export interface CacheEntriesResponse {
  items: CacheEntry[]
  total: number
  page: number
  pageSize: number
}

export interface CleanupTask {
  id: number
  name: string
  strategy: 'lru' | 'capacity'
  thresholdSeconds: number
  thresholdBytes: number
  enabled: boolean
  cronIntervalSec: number
  lastRunAt: number
  lastStatus: string
  lastFreedBytes: number
  lastFreedCount: number
  createdAt: number
}

export interface CleanupReport {
  taskId: number
  strategy: string
  freedCount: number
  freedBytes: number
  beforeCount: number
  beforeBytes: number
  afterCount: number
  afterBytes: number
  durationMs: number
}

export interface UpstreamHealth {
  url: string
  reachable: boolean
  latencyMs: number
  error?: string
  lastChecked: number
}

// === SteamCMD DNS 启动器（PRD §9.3） ===

export interface DNSConfig {
  id: number
  enabled: boolean
  listenAddr: string
  upstream: string
  answerIp: string
  domainRules: string[]
  createdAt: number
  updatedAt: number
}

export interface DNSStats {
  totalQueries: number
  hitQueries: number
  missQueries: number
  blockedQueries: number
  lastQueryAt: number // unix seconds, 0 = never
  lastError: string
}

export interface DNSConfigResponse {
  config: DNSConfig
  stats: DNSStats
  listening: boolean
}

export interface DNSConfigPatch {
  enabled?: boolean
  listenAddr?: string
  upstream?: string
  answerIp?: string
  domainRules?: string[]
}

export interface DNSTestAnswer {
  name: string
  type: number
  ttl: number
  data: string
}

export interface DNSTestResponse {
  domain: string
  matched: boolean
  server: string
  rcode: string
  answers: DNSTestAnswer[]
  latencyMs: number
  error?: string
}

// === SteamCMD AppID 管理（PRD §9.3.3） ===

export interface SteamAppID {
  id: number
  appId: number
  name: string
  loginType: 'anonymous' | 'account'
  installDir: string
  enabled: boolean
  lastPreheatAt: number
  lastPreheatStatus: 'ok' | 'error' | 'running' | 'skipped' | ''
  lastPreheatMessage: string
  lastPreheatDurationMs: number
  cacheBytesEstimate: number
  hitCount: number
  missCount: number
  createdAt: number
  updatedAt: number
}

export interface SteamAppIDCreate {
  appId: number
  name: string
  loginType?: 'anonymous' | 'account'
  installDir?: string
  enabled?: boolean
}

export interface SteamAppIDPatch {
  name?: string
  loginType?: 'anonymous' | 'account'
  installDir?: string
  enabled?: boolean
  cacheBytesEstimate?: number
}

export interface SteamAppIDResponse {
  items: SteamAppID[]
  total: number
}

export interface PreheatResponse {
  appId: number
  status: 'ok' | 'error' | 'running' | 'skipped'
  message: string
  durationMs: number
  commandLine?: string
}

// === 通用预热任务（PRD §9.2.3 / §9.5.5） ===

export interface PreheatTask {
  id: number
  name: string
  kind: 'docker' | 'steam' | 'resource'
  targets: string[]
  status: 'pending' | 'running' | 'done' | 'error' | 'canceled'
  progressTotal: number
  progressDone: number
  progressBytes: number
  errorMessage: string
  cronExpression: string
  enabled: boolean
  nextRunAt: number
  lastRunAt: number
  lastDurationMs: number
  retryCount: number
  maxRetries: number
  createdAt: number
  updatedAt: number
}

export interface PreheatItem {
  id: number
  taskId: number
  target: string
  status: 'pending' | 'running' | 'done' | 'error' | 'skipped'
  errorMessage: string
  bytesAdded: number
  startedAt: number
  finishedAt: number
}

export interface PreheatTaskResponse {
  items: PreheatTask[]
  total: number
}

export interface PreheatItemResponse {
  items: PreheatItem[]
  total: number
}

export interface PreheatTaskCreate {
  name: string
  kind: 'docker' | 'steam' | 'resource'
  targets: string[]
  cronExpression?: string
  maxRetries?: number
  enabled?: boolean
}

// === 诊断中心（PRD §9.7） ===

export type DiagStatus = 'ok' | 'warning' | 'error'

export interface DiagCheckResult {
  name: string
  status: DiagStatus
  message: string
  fix?: string
  detail?: string
}

export interface DiagReport {
  playbook: 'docker_pull' | 'steamcmd_dns' | 'reverse_proxy'
  title: string
  summary: DiagStatus
  checks: DiagCheckResult[]
}

export interface DiagFullReport {
  playbooks: DiagReport[]
  generatedAt: number
  cnchVersion: string
}
